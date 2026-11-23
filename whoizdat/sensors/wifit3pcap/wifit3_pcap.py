#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "wifit3 @ git+https://github.com/derv82/wifit3@06c9912a0069b042db8173bcdb8ce08d7074620a",
# ]
# ///
#
# wifit3-pcap: capture with every USB Wi-Fi card that wifit3 drives, and write
# one standard pcap stream (802.11 + radiotap) for other programs.
#
# Copyright (c) 2026 the who_iz_dat authors.
# SPDX-License-Identifier: GPL-2.0-only
#
# This program uses wifit3 (GPL-2.0-only), so it is GPL-2.0-only too. It is a
# separate program from who_iz_dat; they talk only through the pcap stream.
"""wifit3-pcap: one pcap stream from all USB Wi-Fi cards that wifit3 drives.

It uses the wifit3 user-space drivers, so it needs no kernel driver and works
on macOS, Linux and Windows. The cards share the channel list: each card hops
only its part, so two cards cover the channels two times as fast.

Output is a classic pcap stream, link type 127 (802.11 + radiotap). Each frame
has a radiotap header with the channel, the signal level (dBm) and the card
number as the antenna index. Any pcap reader can use it:

    wifit3_pcap.py | whoizdat wifi -pcap - | whoizdat serve -
    wifit3_pcap.py | tshark -r - -Y wlan.fc.type_subtype==8
    wifit3_pcap.py -w capture.pcap
    wifit3_pcap.py --fifo /tmp/wifi.pipe   # then: wireshark -k -i /tmp/wifi.pipe

It is also a Wireshark extcap program (see the README).

Limits, from wifit3: the stream has management and data frames. Control
frames (ACK, RTS, CTS), 4-address (WDS) frames and frames with a bad FCS are
not in it. Frames have no FCS. The time stamp is the time this program got
the frame, not a hardware time stamp. It never transmits.
"""
from __future__ import annotations

import argparse
import asyncio
import contextlib
import logging
import os
import queue
import signal
import stat
import struct
import sys
import threading
import time
from typing import BinaryIO, Callable, Iterable, List, Optional

LINKTYPE_RADIOTAP = 127
SNAPLEN = 65535
CANADA_CHANNELS = "1-11,36-48,149-165"
EXTCAP_IFACE = "wifit3"

log = logging.getLogger("wifit3-pcap")


# ----- pcap and radiotap: pure functions, no wifit3 ---------------------------

def pcap_header(linktype: int = LINKTYPE_RADIOTAP) -> bytes:
    """Classic pcap global header, microsecond time stamps, host byte order little endian."""
    return struct.pack("<IHHiIII", 0xA1B2C3D4, 2, 4, 0, 0, SNAPLEN, linktype)


def pcap_record(data: bytes, ts: float) -> bytes:
    sec = int(ts)
    usec = int(round((ts - sec) * 1_000_000))
    if usec >= 1_000_000:
        sec, usec = sec + 1, usec - 1_000_000
    n = min(len(data), SNAPLEN)
    return struct.pack("<IIII", sec, usec, n, len(data)) + data[:n]


def channel_mhz(channel: Optional[int]) -> int:
    """Centre frequency of a 2.4 or 5 GHz channel number, or 0 when unknown."""
    if not channel:
        return 0
    if channel == 14:
        return 2484
    if 1 <= channel <= 13:
        return 2407 + 5 * channel
    if 32 <= channel <= 177:
        return 5000 + 5 * channel
    return 0


def radiotap(channel: Optional[int], rssi: Optional[int], antenna: Optional[int]) -> bytes:
    """Radiotap header with Channel (bit 3), dBm antenna signal (bit 5), antenna (bit 11)."""
    present = 0
    body = b""
    mhz = channel_mhz(channel)
    if mhz:
        present |= 1 << 3
        flags = 0x0080 if mhz < 3000 else 0x0100   # 2 GHz or 5 GHz spectrum
        body += struct.pack("<HH", mhz, flags)       # offset 8, 2-byte aligned
    if rssi is not None and -128 <= rssi <= 127 and rssi != 0:
        present |= 1 << 5
        body += struct.pack("<b", rssi)
    if antenna is not None and 0 <= antenna <= 255:
        present |= 1 << 11
        body += struct.pack("<B", antenna)
    return struct.pack("<BBHI", 0, 0, 8 + len(body), present) + body


def parse_channels(spec: str) -> List[int]:
    """'1-11,36-48' -> [1..11, 36, 40, 44, 48]. A range steps by 1 below 15 and by 4 above."""
    out: List[int] = []
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        lo, sep, hi = part.partition("-")
        a = int(lo)
        b = int(hi) if sep else a
        if b < a:
            raise ValueError(f"bad channel range {part!r}")
        step = 1 if a <= 14 else 4
        out.extend(range(a, b + 1, step))
    return out


class PcapWriter:
    """Writes records from a queue on its own thread, so a slow reader never stops USB reads.
    When the queue is full, it drops the frame and counts it."""

    def __init__(self, out: BinaryIO, maxsize: int = 8192):
        self._out = out
        self._q: "queue.Queue[Optional[bytes]]" = queue.Queue(maxsize=maxsize)
        self.dropped = 0
        self.written = 0
        self.closed = threading.Event()   # set when the reader goes away
        self._thread = threading.Thread(target=self._run, name="pcap-writer", daemon=True)

    def start(self) -> None:
        self._out.write(pcap_header())
        self._out.flush()
        self._thread.start()

    def put(self, record: bytes) -> None:
        try:
            self._q.put_nowait(record)
        except queue.Full:
            self.dropped += 1

    def stop(self) -> None:
        with contextlib.suppress(queue.Full):
            self._q.put(None, timeout=1)
        self._thread.join(timeout=2)

    def _run(self) -> None:
        try:
            while True:
                rec = self._q.get()
                if rec is None:
                    break
                self._out.write(rec)
                self.written += 1
                if self._q.empty():
                    self._out.flush()
            self._out.flush()
        except (BrokenPipeError, ValueError, OSError):
            pass
        self.closed.set()


def frame_record(raw: bytes, channel: Optional[int], rssi: Optional[int], card: int, ts: float) -> bytes:
    return pcap_record(radiotap(channel, rssi, card) + raw, ts)


# ----- wifit3 ------------------------------------------------------------------

def _wifit3():
    try:
        from wifit3.device import manager
        from wifit3.wlan.array import WlanArray
        from wifit3.wlan.dedupe import StreamMerger
    except ImportError as e:  # pragma: no cover - depends on the install
        sys.exit(f"wifit3-pcap: cannot import wifit3 ({e}). Run with: uv run wifit3_pcap.py")
    return manager, WlanArray, StreamMerger


def describe(d) -> str:
    name = " ".join(x for x in (getattr(d, "vendor", None), getattr(d, "product_name", None)) if x)
    return f"{d.bus}:{d.address}  {d.vid:04x}:{d.pid:04x}  {d.chipset}  {name or d.description}"


def select(devs, cards: List[str]):
    if not cards:
        return list(devs)
    out = []
    for d in devs:
        text = f"{d.bus}:{d.address} {d.vid:04x}:{d.pid:04x} {d.chipset} {d.vendor or ''} {d.product_name or ''} {d.description}".lower()
        if any(c.lower() in text for c in cards):
            out.append(d)
    return out


async def capture(out: BinaryIO, *, cards: List[str], channels: List[int], dwell: float,
                  dfs: bool, dedupe: bool, stop: asyncio.Event) -> int:
    manager, WlanArray, StreamMerger = _wifit3()
    devs = select(manager.devices(), cards)
    if not devs:
        log.error("no supported USB Wi-Fi card found%s", " that matches --card" if cards else "")
        return 1

    writer = PcapWriter(out)
    merger = StreamMerger(window=0.3) if dedupe else None
    array = WlanArray()
    ifaces = []
    for i, d in enumerate(devs):
        iface = manager.wlan_iface(d, name=f"wlan{i}")
        if iface is None:
            log.warning("skip %s: wifit3 cannot open it", describe(d))
            continue
        log.info("bring-up %s", describe(d))
        try:
            if not await iface.connect():
                raise RuntimeError("connect returned False")
        except Exception as e:  # each card is independent; one bad card must not stop the rest
            log.warning("skip %s: %s", describe(d), e)
            with contextlib.suppress(Exception):
                await iface.close()
            continue
        card = len(ifaces)
        if merger is not None:
            merger.add_source(iface.name)

        def on_frame(pkt, iface=iface, card=card) -> None:
            now = time.time()
            if merger is not None and not merger.submit(iface.name, pkt.raw, time.monotonic()):
                return
            writer.put(frame_record(pkt.raw, iface.current_channel, pkt.rssi, card, now))

        iface.register_rx_callback(on_frame)
        array.attach(iface)
        ifaces.append(iface)
        log.info("card %d: %s, channels %s", card, iface.name, iface.supported_channels)

    if not ifaces:
        log.error("no card started")
        return 1

    writer.start()
    if len(channels) == 1:
        await array.set_channel(channels[0])
    else:
        await array.start_hopping(channels=channels, interval=dwell, include_dfs_channels=dfs)
    log.info("capturing with %d card(s); stop with Ctrl-C", len(ifaces))

    async def reader_gone() -> None:
        await asyncio.get_running_loop().run_in_executor(None, writer.closed.wait)
        stop.set()

    watch = asyncio.create_task(reader_gone())
    try:
        await stop.wait()
    finally:
        watch.cancel()
        with contextlib.suppress(Exception):
            await array.stop_hopping()
        for iface in ifaces:
            with contextlib.suppress(Exception):
                await iface.close()
        writer.stop()
        log.info("%d frames written, %d dropped (reader too slow)", writer.written, writer.dropped)
    return 0


def run(out: BinaryIO, args) -> int:
    channels = parse_channels(args.channels)
    if args.fixed:
        channels = [args.fixed]

    async def main() -> int:
        stop = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            with contextlib.suppress(NotImplementedError):   # Windows
                loop.add_signal_handler(sig, stop.set)
        return await capture(out, cards=args.card, channels=channels, dwell=args.dwell,
                             dfs=args.dfs, dedupe=args.dedupe, stop=stop)

    return asyncio.run(main())


# ----- Wireshark extcap --------------------------------------------------------

def extcap(args) -> int:
    if args.extcap_interfaces:
        print("extcap {version=1.0}{help=https://github.com/derv82/wifit3}")
        print(f"interface {{value={EXTCAP_IFACE}}}{{display=wifit3 USB Wi-Fi (all cards)}}")
        return 0
    if args.extcap_dlts:
        print(f"dlt {{number={LINKTYPE_RADIOTAP}}}{{name=IEEE802_11_RADIO}}{{display=802.11 plus radiotap}}")
        return 0
    if args.extcap_config:
        print(f"arg {{number=0}}{{call=--channels}}{{display=Channels}}{{type=string}}{{default={CANADA_CHANNELS}}}"
              "{tooltip=For example 1-11,36-48,149-165}")
        print("arg {number=1}{call=--dwell}{display=Time on each channel (s)}{type=double}{default=0.5}")
        print("arg {number=2}{call=--dfs}{display=Include DFS channels}{type=boolflag}{default=false}")
        print("arg {number=3}{call=--card}{display=Only cards that match (text)}{type=string}")
        print("arg {number=4}{call=--dedupe}{display=Remove copies heard by two cards}{type=boolflag}{default=false}")
        return 0
    if args.capture:
        if not args.fifo:
            log.error("--capture needs --fifo")
            return 1
        with open(args.fifo, "wb") as out:
            return run(out, args)
    return 1


# ----- command line ------------------------------------------------------------

def make_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="wifit3_pcap.py", description=__doc__.split("\n\n")[0])
    p.add_argument("--list", action="store_true", help="list the supported cards on the USB bus and stop")
    p.add_argument("--card", action="append", default=[],
                   help="use only cards that match this text (bus:address, vid:pid, chipset, name); repeat")
    p.add_argument("--channels", default=CANADA_CHANNELS, help=f"channels to hop (default, Canada: {CANADA_CHANNELS})")
    p.add_argument("--fixed", type=int, help="stay on this one channel")
    p.add_argument("--dwell", type=float, default=0.5, help="seconds on each channel (default 0.5)")
    p.add_argument("--dfs", action="store_true", help="include DFS channels in the hop")
    p.add_argument("--dedupe", action="store_true", help="write a frame only once when two cards hear it")
    p.add_argument("-w", dest="write", default="-", help='output file, or "-" for stdout (default)')
    p.add_argument("--fifo", help="named pipe to create (if needed) and write to")
    p.add_argument("-v", "--verbose", action="store_true", help="debug messages from wifit3")
    # Wireshark extcap interface
    p.add_argument("--extcap-interfaces", action="store_true", help=argparse.SUPPRESS)
    p.add_argument("--extcap-dlts", action="store_true", help=argparse.SUPPRESS)
    p.add_argument("--extcap-config", action="store_true", help=argparse.SUPPRESS)
    p.add_argument("--extcap-interface", help=argparse.SUPPRESS)
    p.add_argument("--extcap-version", help=argparse.SUPPRESS)
    p.add_argument("--extcap-capture-filter", help=argparse.SUPPRESS)
    p.add_argument("--capture", action="store_true", help=argparse.SUPPRESS)
    return p


def open_output(args) -> BinaryIO:
    if args.fifo:
        if not os.path.exists(args.fifo):
            os.mkfifo(args.fifo, 0o600)
        elif not stat.S_ISFIFO(os.stat(args.fifo).st_mode):
            sys.exit(f"wifit3-pcap: {args.fifo} exists and is not a named pipe")
        log.info("waiting for a reader on %s", args.fifo)
        return open(args.fifo, "wb")
    if args.write == "-":
        if sys.stdout.isatty():
            sys.exit("wifit3-pcap: stdout is a terminal; pipe it to a program or use -w FILE")
        return sys.stdout.buffer
    return open(args.write, "wb")


def main(argv: Optional[Iterable[str]] = None) -> int:
    args, _unknown = make_parser().parse_known_args(argv)   # Wireshark can add options
    logging.basicConfig(stream=sys.stderr, level=logging.DEBUG if args.verbose else logging.INFO,
                        format="wifit3-pcap %(levelname)s %(message)s")
    if not args.verbose:
        logging.getLogger("wifit3").setLevel(logging.WARNING)
    if args.extcap_interfaces or args.extcap_dlts or args.extcap_config or args.capture:
        return extcap(args)
    if args.list:
        manager, _, _ = _wifit3()
        devs = manager.devices()
        for d in devs:
            print(describe(d))
        if not devs:
            print("no supported USB Wi-Fi card found", file=sys.stderr)
        return 0 if devs else 1
    out = open_output(args)
    try:
        return run(out, args)
    finally:
        with contextlib.suppress(Exception):
            if out is not sys.stdout.buffer:
                out.close()


if __name__ == "__main__":
    sys.exit(main())

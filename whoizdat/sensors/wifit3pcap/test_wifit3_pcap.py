# SPDX-License-Identifier: GPL-2.0-only
"""Tests for wifit3_pcap with a fake wifit3. Run: python3 -m unittest test_wifit3_pcap.py"""
import asyncio
import io
import os
import shutil
import struct
import subprocess
import sys
import types
import unittest
from dataclasses import dataclass

sys.path.insert(0, os.path.dirname(__file__))
import wifit3_pcap as w  # noqa: E402

BEACON = (bytes([0x80, 0, 0, 0]) + b"\xff" * 6 + bytes.fromhex("60601f112233") * 2 + b"\0\0"
          + b"\0" * 12 + bytes([0, 4]) + b"home" + bytes([3, 1, 6]))
PROBE = (bytes([0x40, 0, 0, 0]) + b"\xff" * 6 + bytes.fromhex("daa119010203") + b"\xff" * 6 + b"\0\0"
         + bytes([0, 6]) + b"Coffee")


class PureTest(unittest.TestCase):
    def test_header(self):
        h = w.pcap_header()
        self.assertEqual(len(h), 24)
        self.assertEqual(struct.unpack("<IHHiIII", h), (0xA1B2C3D4, 2, 4, 0, 0, 65535, 127))

    def test_record(self):
        r = w.pcap_record(b"abc", 10.9999996)
        self.assertEqual(struct.unpack("<IIII", r[:16]), (11, 0, 3, 3))
        self.assertEqual(r[16:], b"abc")

    def test_radiotap(self):
        rt = w.radiotap(6, -42, 1)
        ver, pad, ln, present = struct.unpack("<BBHI", rt[:8])
        self.assertEqual((ver, ln, present), (0, 14, (1 << 3) | (1 << 5) | (1 << 11)))
        self.assertEqual(struct.unpack("<HHbB", rt[8:]), (2437, 0x0080, -42, 1))
        self.assertEqual(struct.unpack("<HH", w.radiotap(36, None, None)[8:12]), (5180, 0x0100))
        self.assertEqual(len(w.radiotap(None, None, None)), 8)

    def test_channels(self):
        self.assertEqual(w.parse_channels("1-3, 36-44,149"), [1, 2, 3, 36, 40, 44, 149])
        self.assertEqual(w.channel_mhz(14), 2484)
        self.assertEqual(w.channel_mhz(0), 0)
        with self.assertRaises(ValueError):
            w.parse_channels("11-1")


# ----- a fake wifit3 with two cards ------------------------------------------------

@dataclass
class Dev:
    bus: int
    address: int
    vid: int
    pid: int
    chipset: str
    description: str
    vendor: str = "ALFA"
    product_name: str = ""


@dataclass
class Pkt:
    raw: bytes
    rssi: int


class FakeIface:
    def __init__(self, dev, name):
        self.dev, self.name, self.current_channel = dev, name, None
        self.cbs, self.closed = [], False
        self.supported_channels = [1, 6, 11, 36, 40]

    async def connect(self, progress_cb=None):
        return self.dev.chipset != "broken"

    def register_rx_callback(self, cb):
        self.cbs.append(cb)

    def emit(self, raw, rssi):
        for cb in self.cbs:
            cb(Pkt(raw, rssi))

    async def close(self):
        self.closed = True


class FakeArray:
    last = None

    def __init__(self):
        self.members, self.hop = [], None
        FakeArray.last = self

    def attach(self, iface):
        self.members.append(iface)

    async def start_hopping(self, channels, interval, include_dfs_channels):
        self.hop = (channels, interval, include_dfs_channels)
        # card 0 is on channel 6, card 1 on channel 36; both hear the same beacon
        self.members[0].current_channel = 6
        self.members[1].current_channel = 36
        self.members[0].emit(BEACON, -40)
        self.members[1].emit(BEACON, -55)
        self.members[1].emit(PROBE, -60)

    async def set_channel(self, ch):
        for m in self.members:
            m.current_channel = ch

    async def stop_hopping(self):
        pass


class FakeMerger:
    def __init__(self, window):
        self.seen = set()

    def add_source(self, src):
        pass

    def submit(self, src, raw, now):
        new = raw not in self.seen
        self.seen.add(raw)
        return new


def install_fake(devs):
    manager = types.SimpleNamespace(devices=lambda: devs, wlan_iface=lambda d, name: FakeIface(d, name))
    w._wifit3 = lambda: (manager, FakeArray, FakeMerger)


DEVS = [Dev(1, 5, 0x0BDA, 0x8812, "rtl8812au", "AWUS036ACH"),
        Dev(1, 6, 0x0BDA, 0xB812, "rtl8822bu", "RTL8822BU"),
        Dev(1, 7, 0x0E8D, 0x7612, "broken", "broken card")]


def read_pcap(b):
    assert struct.unpack("<I", b[:4])[0] == 0xA1B2C3D4
    off, out = 24, []
    while off < len(b):
        _, _, n, _ = struct.unpack("<IIII", b[off:off + 16])
        out.append(b[off + 16:off + 16 + n])
        off += 16 + n
    return out


async def run_capture(dedupe=False, cards=()):
    out = io.BytesIO()
    stop = asyncio.Event()
    asyncio.get_running_loop().call_later(0.2, stop.set)
    rc = await w.capture(out, cards=list(cards), channels=[1, 6, 11, 36], dwell=0.25,
                         dfs=False, dedupe=dedupe, stop=stop)
    return rc, out.getvalue()


class CaptureTest(unittest.TestCase):
    def setUp(self):
        install_fake(DEVS)

    def test_two_cards_one_stream(self):
        rc, data = asyncio.run(run_capture())
        self.assertEqual(rc, 0)
        frames = read_pcap(data)
        self.assertEqual(len(frames), 3)
        antennas = [f[13] for f in frames]
        self.assertEqual(antennas, [0, 1, 1])           # the broken card was skipped
        self.assertEqual(FakeArray.last.hop, ([1, 6, 11, 36], 0.25, False))
        self.assertTrue(all(m.closed for m in FakeArray.last.members))
        self.go_reader(data)

    def test_dedupe(self):
        _, data = asyncio.run(run_capture(dedupe=True))
        self.assertEqual(len(read_pcap(data)), 2)

    def test_select(self):
        self.assertEqual([d.chipset for d in w.select(DEVS, ["b812"])], ["rtl8822bu"])
        self.assertEqual(len(w.select(DEVS, [])), 3)
        install_fake([])
        rc, _ = asyncio.run(run_capture())
        self.assertEqual(rc, 1)

    def go_reader(self, data):
        """The stream must be readable by whoizdat wifi -pcap -."""
        exe = os.environ.get("WHOIZDAT")
        if not exe or not shutil.which(exe):
            self.skipTest("set WHOIZDAT to a whoizdat binary to test the Go reader")
        p = subprocess.run([exe, "wifi", "-pcap", "-"], input=data, capture_output=True, check=True)
        lines = p.stdout.decode().splitlines()
        self.assertEqual(len(lines), 3, p.stderr.decode())
        self.assertIn('"name":"home"', lines[0])
        self.assertIn('"rssi":-40', lines[0])
        self.assertIn('"mhz":2437', lines[0])
        self.assertIn('"mhz":5180', lines[1])
        self.assertIn('"frame":"probe-req"', lines[2])


class ExtcapTest(unittest.TestCase):
    def run_main(self, *args):
        old = sys.stdout
        sys.stdout = io.StringIO()
        try:
            rc = w.main(list(args))
            return rc, sys.stdout.getvalue()
        finally:
            sys.stdout = old

    def test_queries(self):
        rc, out = self.run_main("--extcap-interfaces", "--extcap-version=4.4")
        self.assertEqual(rc, 0)
        self.assertIn("interface {value=wifit3}", out)
        rc, out = self.run_main("--extcap-dlts", "--extcap-interface", "wifit3")
        self.assertIn("{number=127}", out)
        rc, out = self.run_main("--extcap-config", "--extcap-interface", "wifit3")
        self.assertIn("{call=--channels}", out)


if __name__ == "__main__":
    unittest.main()

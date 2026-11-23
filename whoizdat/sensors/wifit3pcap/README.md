# wifit3-pcap

One standard pcap stream from all the USB Wi-Fi cards that
[wifit3](https://github.com/derv82/wifit3) drives. You plug in the cards, and
other programs (who_iz_dat, Wireshark, tshark, tcpdump, Kismet, scripts) read
the stream. No kernel driver, no driver install. It works on macOS (Apple
Silicon and Intel), Linux and Windows, because wifit3 drives the cards from
user space over libusb.

License: GPL-2.0-only, because it uses wifit3. It is a separate program from
who_iz_dat (MIT); they connect only through the pcap stream.

## What it does

- It finds every supported card on the USB bus and starts each one with the
  wifit3 driver. A card that fails is skipped; the others continue.
- The cards share the channel list (the wifit3 "spread" hop): each card hops
  only its part, so two cards cover the list two times as fast.
- It writes a classic pcap stream, link type 127 (802.11 + radiotap). Each
  frame has a radiotap header with the channel frequency, the signal (dBm),
  and the card number in the antenna field. In Wireshark, `radiotap.antenna`
  tells you which card heard the frame.
- It never transmits.

Limits, from wifit3: the stream has management and data frames. Control frames
(ACK, RTS, CTS), 4-address (WDS) frames and frames with a bad FCS are not in
it. The frames have no FCS. The time stamp is the time this program got the
frame, not a hardware time.

## Run it

You need [uv](https://docs.astral.sh/uv/). The first run downloads wifit3 (at
a fixed commit) and its Python packages into the uv cache.

```bash
./wifit3_pcap.py --list
```

On macOS, the first run of a card shows a USB "Allow" dialog. On Linux, run
wifit3 once and use its Install button for the card (it adds a udev rule and
blocks the kernel driver), or run this program with sudo.

Examples:

```bash
./wifit3_pcap.py | ../../whoizdat wifi -pcap - | ../../whoizdat serve -catalog sigs.rsi -
./wifit3_pcap.py -w capture.pcap
./wifit3_pcap.py | tshark -r - -Y "wlan.fc.type_subtype == 4"
./wifit3_pcap.py --fifo /tmp/wifi.pipe          # then: wireshark -k -i /tmp/wifi.pipe
./wifit3_pcap.py --card b812 --fixed 6           # one card, one channel
./wifit3_pcap.py --channels 1-13,36-165 --dfs    # all channels, with DFS
```

The default channels are for Canada with no DFS: `1-11,36-48,149-165`.
`--dedupe` writes a frame only once when two cards hear it.

## Wireshark (extcap)

The program is also a Wireshark extcap, so the cards show as one capture
interface ("wifit3 USB Wi-Fi (all cards)") with its options in the
interface settings.

1. In Wireshark, open About Wireshark, Folders, and find "Personal Extcap
   path".
2. Copy `wifit3_pcap.py` there and make it executable.
3. Wireshark starts extcap programs with a short PATH, so it may not find
   `uv`. If the interface does not show, change the first line of the copy to
   the full path of uv, for example
   `#!/usr/bin/env -S /Users/you/.local/bin/uv run --script`.
4. Restart Wireshark.

## Status

- Tested here with a fake wifit3: the pcap and radiotap bytes, the card
  selection, the channel split, deduplication, the extcap answers, and that
  `whoizdat wifi -pcap -` reads the stream.
- Tested here with the real wifit3 at the pinned commit: the imports and the
  calls match its API, and `uv run --script` installs it. There was no USB
  card on that computer, so no real capture was tested.

```bash
python3 -m unittest test_wifit3_pcap.py
```

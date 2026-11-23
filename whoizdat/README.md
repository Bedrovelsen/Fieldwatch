# who_iz_dat

A passive radio awareness tool for a fixed site, such as a home. It listens to
Wi-Fi in monitor mode and, later, Bluetooth and SDR receivers. It shows what is
near the property, learns a home baseline, and gives an alert when a new radio
or a radio of interest comes near. It never transmits.

It is a Go port in progress of the Fieldwatch Android app in this repository.
It reads the same radio-signatures (RSI-1) catalog files.

- Go standard library only. No cgo. One static binary for Linux and macOS.
- Web UI: `html/template`, semantic HTML, classless CSS, fixi 0.9.4, and a
  20-line fixi extension for polling. Every page also works with no JavaScript.
- No daemons. Programs connect through JSONL on pipes.

## Status

| Part | State |
|---|---|
| JSONL observation contract (`obs`) | Done |
| Signature matching on RSI-1 catalogs (`sig`) | Done, tested on the stock catalog. Cluster signatures and the Fieldwatch de-duplication rules (for example iBeacon over a product row) are not ported yet. The stock catalog has no cluster signatures. |
| Wi-Fi: radiotap and 802.11 parsing, pcap and pcapng (`wifi`) | Done, tested. Beacons, probe responses, probe requests, client data frames, deauthentication. |
| Wi-Fi live capture, Linux (AF_PACKET, no libpcap) | Done. Not yet tested with a real adapter. |
| Channel hopping | Done, with `iw` as a subprocess. |
| Engine: radio table, home baseline, alerts (`engine`) | Done, tested. Alerts for new radios, signatures with an attention note, and deauthentication bursts. |
| Web UI: Live, radio detail, Alerts, Signatures (`web`) | Done, tested in Chromium. |
| Sub-GHz sensors through rtl_433 (`ism`, `whoizdat rtl433`) | Done, tested with sample rtl_433 JSON. Not tested with a LimeSDR. |
| Bluetooth LE on macOS (`sensors/macble`, Swift) | Written, not compiled or tested: this work was done on Linux. |
| Bluetooth LE on Linux | Not started. See the plan. |
| LimeSDR spectrum, ADS-B | Not started. See the plan. |
| Map (MapLibre, distance rings) | Done, tested in Chromium with demo data. The basemap file step is not tested. |
| Wi-Fi on macOS with Alfa cards (`sensors/wifit3pcap`, wifit3 bridge, GPL-2.0) | Written and tested with a fake wifit3 and with the real wifit3 API. Not tested with a card. |

## Build

```bash
cd whoizdat
go test ./...
go build -o whoizdat ./cmd/whoizdat
```

For a Raspberry Pi: `GOOS=linux GOARCH=arm64 go build ./cmd/whoizdat`.
For a Mac: `GOOS=darwin GOARCH=arm64 go build ./cmd/whoizdat`.

## Try it with no hardware

```bash
./whoizdat demo | ./whoizdat serve -catalog ../dist/fieldwatch-signatures.rsi -learn 20s -
```

Open http://127.0.0.1:8080/. During the first 20 seconds, the demo radios
become the home baseline. After that, visitors arrive: a Tile tracker, a
Cradlepoint vehicle router, a Flipper Zero, a Tesla, and a phone that asks for
a coffee shop network. At 80 seconds there is a deauthentication burst.

## Use it on Linux with an Alfa adapter

Driver notes (check on your computer):

- AWUS036ACM (MediaTek MT7612U) uses the `mt76x2u` driver that is in the
  Linux kernel. Monitor mode works well with it. Use this adapter first.
- AWUS036ACH (Realtek RTL8812AU) needs a driver from outside the kernel on
  most distributions (for example `aircrack-ng/rtl8812au` with DKMS). New
  kernels also have `rtw88_8812au`; its monitor mode may be less complete.

To see which adapter you have: `lsusb` on Linux, or
`system_profiler SPUSBDataType` on macOS. `0e8d:7612` is MT7612U (ACM) and
`0bda:8812` is RTL8812AU (ACH).


The capture program and the web server are separate processes. Only the
capture program gets capture rights. The web server has no rights.

1. Give the binary the rights to capture and to change the channel, one time:

   ```bash
   sudo setcap cap_net_raw,cap_net_admin+ep ./whoizdat
   ```

   `iw` changes the channel. It runs as a child process and does not get these
   rights. If channel hopping fails, run `whoizdat wifi` with `sudo`, or give
   `iw` the same capability.

2. Put the adapter in monitor mode one time (or use `-monitor`):

   ```bash
   sudo ip link set wlan1 down && sudo iw dev wlan1 set type monitor && sudo ip link set wlan1 up
   ```

3. Run it:

   ```bash
   ./whoizdat wifi -iface wlan1 | ./whoizdat serve -catalog ../dist/fieldwatch-signatures.rsi -
   ```

   The default channels are for Canada with no DFS channels:
   `1-11,36-48,149-165`. With two adapters, give each one half:

   ```bash
   ( ./whoizdat wifi -iface wlan1 -hop 1-11 & ./whoizdat wifi -iface wlan2 -hop 36-48,149-165 ) | ./whoizdat serve -catalog sigs.rsi -
   ```

The home baseline is in `~/.config/whoizdat/known.json` (Linux) or
`~/Library/Application Support/whoizdat/known.json` (macOS). Delete the file
to learn again.

To keep a record, put `tee` in the pipe:
`./whoizdat wifi -iface wlan1 | tee -a capture.jsonl | ./whoizdat serve -`.
To replay it later: `./whoizdat serve capture.jsonl`.

## Sub-GHz sensors with the LimeSDR (rtl_433)

rtl_433 decodes tire pressure sensors (TPMS) of cars that pass, weather
stations, door and motion sensors, and key fobs. In Canada, TPMS and most key
fobs use 315 MHz, and many home sensors use 433.92 MHz. The LimeSDR cannot
receive both at the same time, so rtl_433 hops between them.

rtl_433 must be built with SoapySDR, and SoapySDR needs the LimeSDR module
(SoapyLMS7 from LimeSuite). I did not test this command with a LimeSDR:

```bash
rtl_433 -d driver=lime -f 315M -f 433.92M -H 30 -M level -F json | ./whoizdat rtl433 | ./whoizdat serve -
```

The signal level from rtl_433 is relative to the receiver (dB), not dBm.
There are no signatures for these sensors; the baseline and "new" alerts
apply. A car that parks near the house shows as four new tire sensors.

## Bluetooth on macOS

The built-in Bluetooth of a Mac works through CoreBluetooth. That is only
available from Swift or Objective-C, so the sensor is a separate small
program in `sensors/macble`. It needs the Xcode command line tools:

```bash
cd sensors/macble
swiftc -O -o whoizdat-ble main.swift -Xlinker -sectcreate -Xlinker __TEXT -Xlinker __info_plist -Xlinker Info.plist
./whoizdat-ble | ../../whoizdat serve -catalog ../../../dist/fieldwatch-signatures.rsi -
```

macOS does not give the real Bluetooth address. Each radio has an id that
this Mac makes, so address (OUI) rules do not apply. Name, company and service
rules do. These radios count as random addresses, so they do not raise "new"
alerts unless you use `-alert-random`. Signature alerts still work.

All sensors can feed one server at the same time:

```bash
( ./whoizdat-ble & ./whoizdat demo ) | ./whoizdat serve -
```

## Wi-Fi on macOS

The macOS kernel has no monitor-mode driver for these Alfa adapters, so
`whoizdat wifi -iface` works only on Linux. There are three ways:

- Sensor on Linux, UI on the Mac. The sensor computer needs only the binary:

  ```bash
  ssh pi ./whoizdat wifi -iface wlan1 | ./whoizdat serve -catalog sigs.rsi -
  ```

- The built-in Mac Wi-Fi with tcpdump. Monitor mode with `-I` may work on
  some Macs and macOS versions. I did not test this.

  ```bash
  sudo tcpdump -I -i en0 -w - | ./whoizdat wifi -pcap - | ./whoizdat serve -
  ```

- The wifit3 bridge (recommended). wifit3 drives the cards from user space,
  with no kernel driver. It grades your RTL8812AU (AWUS036ACH) 91% and
  RTL8822BU (`0bda:b812`) 90%. See `sensors/wifit3pcap/README.md`:

  ```bash
  sensors/wifit3pcap/wifit3_pcap.py | ./whoizdat wifi -pcap - | ./whoizdat serve -catalog sigs.rsi -
  ```

## Map

```bash
./whoizdat serve -site 45.4215,-75.6972 -basemap area.pmtiles ...
```

`-site` is the position of the sensor (the house), at the centre of the map.
One sensor gives a signal level but no direction. So each radio is a dot at
its estimated distance, in a direction that the program makes up from a hash
of its id (the direction stays the same, but it is not real). Rings show
10, 25, 50, 100 and 200 m. Click a dot to see the band of possible distances.

The distance uses the log-distance model, d = 10^((ref - rssi) / (10 n)), with
the smoothed level of the radio. Defaults: ref -40 dBm at 1 m for Wi-Fi, -59
dBm for Bluetooth, n = 2.7, and a band from n = 3.5 (near) to n = 2.0 (far).
Change them with `-ref-wifi`, `-ref-ble` and `-path-n`. To calibrate: put a
known radio at a known distance and change `-ref-wifi` until the map agrees.
Sub-GHz sensors are not on the map, because rtl_433 levels are not in dBm.

The basemap is optional. With no basemap, the map shows the rings on a plain
background. To make a basemap of your area one time (needs internet once, and
the `pmtiles` program from https://github.com/protomaps/go-pmtiles):

```bash
pmtiles extract https://build.protomaps.com/20261101.pmtiles area.pmtiles \
  --bbox=-75.705,45.417,-75.690,45.426 --maxzoom=15
```

Use the date of a recent build from https://maps.protomaps.com/builds/ and a
box around your area (west,south,east,north). For four blocks the file is
small. The map has no text labels, so it needs no font files. The data is
from OpenStreetMap (ODbL). I could not reach the Protomaps server from the
computer where I wrote this, so this step is not tested.

MapLibre GL JS 4.7.1 (CSP build) and pmtiles.js 3.2.1 are in
`web/static/maplibre` (BSD-3-Clause). Only the Map page loads them.

## Remote access with Tailscale

The UI listens on 127.0.0.1 by default. It refuses Host names that it does
not know (protection against DNS rebinding), and it refuses POST requests from
other sites. To use it from a phone on your tailnet:

```bash
./whoizdat serve -allow-host box.tail1234.ts.net ... &
tailscale serve 8080
```

## Files

```
cmd/whoizdat  commands: wifi, demo, serve
obs           Observation type and JSONL read and write
sig           RSI-1 catalog and matcher
wifi          radiotap, 802.11, pcap, pcapng, AF_PACKET capture, channel hopping
engine        radio table, home baseline, alerts
web           handlers, templates (layout, pages, partials), static files
ism           rtl_433 JSON to observations
geo           distance model and map geometry
sensors/macble    macOS Bluetooth sensor (Swift, separate program)
sensors/wifit3pcap  wifit3 bridge: USB Wi-Fi cards to a pcap stream (Python, GPL-2.0)
```

`web/static/fixi.js` is fixi 0.9.4 from
https://raw.githubusercontent.com/bigskysoftware/fixi/refs/tags/0.9.4/fixi.js
(SHA-256 `acfb84e7c6c2c8cad9dfba386c1c97e1ea60040d5b01e4f2969ed3c4e5d587dd`).

See [PLAN.md](PLAN.md) for the three plans and the next phases.

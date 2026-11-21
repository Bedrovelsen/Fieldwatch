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
| Bluetooth LE | Not started. See the plan. |
| LimeSDR | Not started. See the plan. |
| Map | Not started. See the plan. |

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
   ./whoizdat wifi -iface wlan1 -hop 1-13,36-48 | ./whoizdat serve -catalog ../dist/fieldwatch-signatures.rsi -
   ```

The home baseline is in `~/.config/whoizdat/known.json` (Linux) or
`~/Library/Application Support/whoizdat/known.json` (macOS). Delete the file
to learn again.

To keep a record, put `tee` in the pipe:
`./whoizdat wifi -iface wlan1 | tee -a capture.jsonl | ./whoizdat serve -`.
To replay it later: `./whoizdat serve capture.jsonl`.

## macOS

macOS cannot use an Alfa adapter in monitor mode with this program. There are
three ways:

- Sensor on Linux, UI on the Mac. The sensor computer needs only the binary:

  ```bash
  ssh pi ./whoizdat wifi -iface wlan1 | ./whoizdat serve -catalog sigs.rsi -
  ```

- The built-in Mac Wi-Fi with tcpdump. Monitor mode with `-I` may work on
  some Macs and macOS versions. I did not test this.

  ```bash
  sudo tcpdump -I -i en0 -w - | ./whoizdat wifi -pcap - | ./whoizdat serve -
  ```

- A user-space USB driver, the method of wifit3. See phase 6 in the plan.

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
```

`web/static/fixi.js` is fixi 0.9.4 from
https://raw.githubusercontent.com/bigskysoftware/fixi/refs/tags/0.9.4/fixi.js
(SHA-256 `acfb84e7c6c2c8cad9dfba386c1c97e1ea60040d5b01e4f2969ed3c4e5d587dd`).

See [PLAN.md](PLAN.md) for the three plans and the next phases.

# Plan

## Decisions so far

| Question | Answer |
|---|---|
| Hardware | LimeSDR-USB (micro USB), Alfa Wi-Fi adapters, a fixed site (home lab) |
| Bluetooth source | Any: host Bluetooth, an nRF52840 dongle, or the LimeSDR |
| Purpose | Awareness of the property and the area near it |
| New Wi-Fi data | Yes: clients, probe requests, deauthentication |
| Transmit | Never. Receive only. |
| Deployment | Adapters on the same computer first. A field sensor with the UI on another computer is also possible. Remote access with Tailscale. |
| Privileges | As few as possible |
| Maps | Important. Offline tiles. Download only for a new place. |
| Front end | fixi first, hyperscript only if necessary |
| Compatibility with Fieldwatch logs and settings | Not necessary |
| Name | who_iz_dat |
| Dependencies | Standard library; `golang.org/x/sys` is acceptable |

## The three plans

**A. One Go program (chosen).** All parts are in Go and in one binary. The
parts connect only through the JSONL `obs.Observation` contract. Today the
parts are separate commands on a pipe, so the capture program has rights and
the web server has none.

**B. A pipeline of existing tools.** Each sensor is an existing program
(tcpdump, readsb, rtl_433, gpsd) with a small adapter to JSONL. The Go core
only reads JSONL and serves the UI. This plan is the best for a sensor on a
Raspberry Pi and a UI on a Mac.

**C. Keep the Kotlin domain code.** Make `app/.../domain` a plain JVM module,
serve the same templates with the JDK `com.sun.net.httpserver`, and read JSONL.
You keep 44,000 lines and 65 test files, but you need a JVM.

The code uses A, with B where an existing tool is much better than new code.
This is possible because a sensor only has to write JSONL. The Kotlin tests
from C are the specification for each Go port.

## Phases

1. **Done.** JSONL contract, signature matcher, Wi-Fi parser and capture,
   engine with home baseline and alerts, web UI, demo data.
2. **Test with real hardware.** Alfa adapter on Linux. Find the channel list
   and dwell time that suit the site. Several adapters: one `whoizdat wifi`
   for each adapter, each on its own channels, all into one `serve`
   (`serve` reads many inputs).
3. **Bluetooth LE.**
   - Linux host Bluetooth: an HCI socket with `syscall` (no BlueZ library).
   - Both systems: an nRF52840 dongle with sniffer firmware on a serial port
     (`golang.org/x/sys` for termios). This hears all three advertising
     channels and works the same on Linux and macOS.
   - Port the BLE parts of Fieldwatch: advertisement parser, Find My and
     tracker detection, Remote ID over BLE, floods.
4. **LimeSDR-USB.** The LimeSDR is a wide receiver (about 100 kHz to 3.8 GHz).
   Read it through a subprocess, not through cgo:
   - `rtl_433` with SoapySDR (`-d driver=lime`) gives JSON for 315, 433, 868
     and 915 MHz devices: tire pressure sensors of cars that pass, weather
     stations, door and motion sensors, key fobs. A small adapter makes JSONL.
   - `readsb` or `dump1090` with SoapySDR gives ADS-B aircraft positions at
     1090 MHz for the map.
   - Own Go code for a spectrum view and for energy alerts: drone video and
     control links at 2.4 and 5.8 GHz, and the GPS L1 noise floor at
     1575.42 MHz (GPS jammer warning).
   The LimeSDR can listen to only one band at a time, so the program must
   share time between these jobs or you must choose one.
5. **Map.** At a fixed site with one sensor, the program cannot find the
   position of a Wi-Fi or Bluetooth radio. The map shows what has a position:
   the sensors, drones (Remote ID gives the position), aircraft (ADS-B), and
   the known cameras list (`dist/fieldwatch-sensors.geojson.gz`). The tiles are
   a directory of raster tiles for the area, served as static files.
   Note: the OpenStreetMap tile servers do not permit bulk download. Use a
   source that permits it, or make the tiles yourself.
6. **macOS capture with Alfa adapters.** wifit3 drives USB Wi-Fi chipsets from
   user space with recorded start-up sequences, on Linux and Windows. On
   macOS, Go needs IOKit through cgo (or libusb) for this. This is large work
   for each chipset. Do it only if a Linux sensor is not acceptable.
7. **Port more Fieldwatch features** as necessary: trusted networks, tracker
   watch, sits and reports, TAK output, voice alerts (the browser speech API),
   language packs.

## Open questions

1. Which Alfa models (or chipsets) do you have, and how many?
2. Do you agree to buy an nRF52840 dongle for Bluetooth on both systems?
3. Are external tools (rtl_433, readsb, SoapySDR with SoapyLMS7) acceptable
   for the LimeSDR, as in plan B?
4. Which LimeSDR job is first: cars and sensors (rtl_433), aircraft (ADS-B),
   drone links, or the GPS jammer warning?
5. Where do the map tiles come from, and how large is the area?
6. Which country? This sets the legal Wi-Fi channels and the rules about
   recording probe requests.

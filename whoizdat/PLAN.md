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
| Wi-Fi adapters | AWUS036ACH (RTL8812AU) and one more, probably AWUS036ACM (MT7612U); check the USB id |
| Computer | Mac mini M4 |
| Bluetooth | The Mac's built-in Bluetooth (no dongle) |
| First LimeSDR job | Cars and sensors (rtl_433) |
| Map | MapLibre, area of about four blocks |
| Country | Canada |
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
3. **Bluetooth LE.** Mac: done as a Swift sensor, not yet compiled.
   - Linux host Bluetooth: an HCI socket with `syscall` (no BlueZ library).
   - The Mac's built-in Bluetooth: `sensors/macble` (CoreBluetooth). It does
     not give real addresses.
   - Both systems: an nRF52840 dongle with sniffer firmware on a serial port
     (`golang.org/x/sys` for termios). This hears all three advertising
     channels and works the same on Linux and macOS.
   - Port the BLE parts of Fieldwatch: advertisement parser, Find My and
     tracker detection, Remote ID over BLE, floods.
4. **LimeSDR-USB.** The LimeSDR is a wide receiver (about 100 kHz to 3.8 GHz).
   Read it through a subprocess, not through cgo:
   - Done: `whoizdat rtl433`. `rtl_433` with SoapySDR (`-d driver=lime`) gives JSON for 315, 433, 868
     and 915 MHz devices: tire pressure sensors of cars that pass, weather
     stations, door and motion sensors, key fobs. A small adapter makes JSONL.
   - `readsb` or `dump1090` with SoapySDR gives ADS-B aircraft positions at
     1090 MHz for the map.
   - Own Go code for a spectrum view and for energy alerts: drone video and
     control links at 2.4 and 5.8 GHz, and the GPS L1 noise floor at
     1575.42 MHz (GPS jammer warning).
   The LimeSDR can listen to only one band at a time, so the program must
   share time between these jobs or you must choose one.
5. **Map (MapLibre).** Plan:
   - Vendor MapLibre GL JS (the CSP build and its worker file, so the page
     needs no `blob:` or `eval`) and pmtiles.js. Both are BSD-3. Load them only
     on the Map page; the other pages stay small.
   - Basemap: one `.pmtiles` file for the area, cut one time from a Protomaps
     build with `pmtiles extract --bbox`. For four blocks it is small. Go serves
     it with `http.ServeContent`, which supports the HTTP range requests that
     PMTiles uses. No tile server.
   - Optional: aerial photos. Many Canadian cities publish open orthophotos.
   - Labels need font files (glyphs). A map with no labels needs none.
   At a fixed site with one sensor, the program cannot find the
   position of a Wi-Fi or Bluetooth radio. The map shows what has a position:
   the sensors, drones (Remote ID gives the position), aircraft (ADS-B), and
   the known cameras list (`dist/fieldwatch-sensors.geojson.gz`). The tiles are
   a directory of raster tiles for the area, served as static files.
   Note: the OpenStreetMap tile servers do not permit bulk download. Use a
   source that permits it, or make the tiles yourself.
6. **macOS capture with Alfa adapters.** wifit3 (GPL-2.0, Python, PyUSB over
   libusb) runs on Linux, Windows and macOS. It has user-space ports of the
   Linux drivers and grades RTL8812AU (ACH) 91% and MT7612U (ACM) 93%. Its
   documented output is `.pcap` and `.hc22000` files from its TUI; I found no
   documented stream of all frames. Ways to use it:
   - A bridge: a small Python program, separate from who_iz_dat, that uses the
     wifit3 driver code and writes a radiotap pcap stream to stdout, into
     `whoizdat wifi -pcap -`. The bridge must be GPL-2.0. who_iz_dat stays a
     separate program and keeps its own license. This needs a read of the
     wifit3 source first.
   - A Go port of one driver (MT7612U first). This makes who_iz_dat a GPL-2.0
     derivative, and Go needs libusb or IOKit on macOS, which breaks "no cgo".
     It is a large job.
   - A Linux sensor (for example a Raspberry Pi) with the in-kernel MT7612U
     driver.
7. **Port more Fieldwatch features** as necessary: trusted networks, tracker
   watch, sits and reports, TAK output, voice alerts (the browser speech API),
   language packs.

## Law (Canada), not legal advice

The program reads only frame headers and advertisements, not the content of
communications. The Radiocommunication Act (section 9) limits the use and the
disclosure of radio communications that you receive. Keep the records for your
own use, keep them for a short time, and do not publish them.

## Open questions

1. What is the USB id of the second adapter (see the README)?
2. For Wi-Fi on the Mac: the wifit3 bridge (GPL-2.0, separate program), a
   Linux sensor, or both?
3. Do you want labels on the map (needs font files), and do you want aerial
   photos?
4. Where are the sensors (house position), for the map and for the distance
   alerts of Remote ID drones?

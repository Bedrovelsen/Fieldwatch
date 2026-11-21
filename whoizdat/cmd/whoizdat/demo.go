package main

import (
	"context"
	"flag"
	"math/rand/v2"
	"os"
	"time"

	"whoizdat/obs"
)

// demoRadio is one test transmitter. It is on the air from start to stop.
type demoRadio struct {
	o           obs.Observation
	rssi        int
	start, stop time.Duration // stop 0 = always
}

// demoRadios gives a home with its own radios, neighbours, and visitors that
// arrive later. The names and ids match signatures in the stock catalog.
func demoRadios() []demoRadio {
	ap := func(mac, name string, ch int, ies ...string) obs.Observation {
		return obs.Observation{Kind: obs.WiFi, MAC: mac, Frame: obs.FrameBeacon, Name: name, Chan: ch, VendorIEs: ies}
	}
	ble := func(mac, name string, mfg []obs.Mfg, uuids ...string) obs.Observation {
		return obs.Observation{Kind: obs.BLE, MAC: mac, Frame: obs.FrameAdv, Name: name, Mfg: mfg, UUIDs: uuids}
	}
	client := func(mac, bssid string) obs.Observation {
		return obs.Observation{Kind: obs.WiFi, MAC: mac, Frame: obs.FrameData, BSSID: bssid, Chan: 6}
	}
	probe := func(mac, name string) obs.Observation {
		return obs.Observation{Kind: obs.WiFi, MAC: mac, Frame: obs.FrameProbeReq, Name: name}
	}
	s := time.Second
	return []demoRadio{
		// Home
		{o: ap("F8:BB:BF:10:20:30", "eero-home", 6), rssi: -38},
		{o: ap("FA:BB:BF:10:20:31", "eero-guest", 6), rssi: -40},
		{o: client("3C:22:FB:01:02:03", "F8:BB:BF:10:20:30"), rssi: -45},
		{o: client("DA:A1:19:55:66:77", "F8:BB:BF:10:20:30"), rssi: -52},
		{o: ap("90:48:6C:00:11:22", "Ring-door-cam", 1), rssi: -55},
		{o: ble("54:4A:16:00:00:01", "", []obs.Mfg{{CompanyID: 0x004C, DataHex: "1005031C"}}), rssi: -60},
		// Neighbours
		{o: ap("A0:40:A0:33:44:55", "NETGEAR57", 11), rssi: -74},
		{o: ap("00:0F:B3:AA:BB:CC", "Fios-8XK2", 1), rssi: -82},
		{o: ap("C4:41:1E:01:01:01", "", 36), rssi: -78},
		// Visitors after the learning time
		{o: probe("E2:13:44:9A:00:01", "CoffeeShop"), rssi: -66, start: 25 * s, stop: 90 * s},
		{o: ble("C0:00:12:34:56:78", "Tile", nil, "FEED"), rssi: -63, start: 30 * s},
		{o: ap("00:30:44:12:34:56", "Cradlepoint-IBR900", 11), rssi: -69, start: 40 * s, stop: 120 * s},
		{o: ble("0C:FA:22:00:00:09", "Flipper Kiwi", nil), rssi: -58, start: 55 * s},
		{o: ble("E4:00:00:00:AA:01", "", []obs.Mfg{{CompanyID: 555}}), rssi: -71, start: 60 * s},
	}
}

func cmdDemo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	rate := fs.Duration("every", 500*time.Millisecond, "time between rounds")
	deauth := fs.Duration("deauth-at", 80*time.Second, "time of a deauthentication burst; 0 = none")
	fs.Parse(args)

	w := obs.NewWriter(os.Stdout)
	radios := demoRadios()
	begin := time.Now()
	burst := false
	t := time.NewTicker(*rate)
	defer t.Stop()
	for {
		now := time.Now()
		up := now.Sub(begin)
		for _, r := range radios {
			if up < r.start || (r.stop > 0 && up > r.stop) || rand.IntN(3) == 0 {
				continue
			}
			o := r.o
			o.At = now
			o.Sensor = "demo"
			o.RSSI = r.rssi + rand.IntN(7) - 3
			if err := w.Write(o); err != nil {
				return nil // the reader went away
			}
		}
		if *deauth > 0 && !burst && up > *deauth {
			burst = true
			for i := range 30 {
				w.Write(obs.Observation{At: now.Add(time.Duration(i) * 50 * time.Millisecond), Sensor: "demo",
					Kind: obs.WiFi, MAC: "F8:BB:BF:10:20:30", Frame: "deauth", BSSID: "F8:BB:BF:10:20:30", RSSI: -61})
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

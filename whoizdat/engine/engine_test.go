package engine

import (
	"strings"
	"testing"
	"time"

	"whoizdat/obs"
	"whoizdat/sig"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

func newEngine(t *testing.T, dir string) *Engine {
	t.Helper()
	c, err := sig.Load("../../dist/fieldwatch-signatures.rsi")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Config{DataDir: dir, Learn: 10 * time.Minute, NearRSSI: -80}, sig.New(c))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBaselineAndNewAlerts(t *testing.T) {
	dir := t.TempDir()
	e := newEngine(t, dir)
	e.Ingest(obs.Observation{At: at(0), Kind: obs.WiFi, MAC: "00:0F:B3:00:00:01", Frame: obs.FrameBeacon, Name: "home", RSSI: -40})
	e.Ingest(obs.Observation{At: at(11 * time.Minute), Kind: obs.WiFi, MAC: "00:11:22:00:00:02", Frame: obs.FrameBeacon, Name: "van", RSSI: -60})
	e.Ingest(obs.Observation{At: at(12 * time.Minute), Kind: obs.WiFi, MAC: "00:11:22:00:00:03", Frame: obs.FrameBeacon, Name: "far", RSSI: -90})
	e.Ingest(obs.Observation{At: at(13 * time.Minute), Kind: obs.WiFi, MAC: "DA:A1:19:00:00:04", Frame: obs.FrameProbeReq, RSSI: -50})

	if d, _ := e.Get(Key(obs.WiFi, "00:0F:B3:00:00:01")); d.New || d.Role != RoleAP || len(d.Sigs) == 0 {
		t.Errorf("learned AP: %+v", d)
	}
	if d, _ := e.Get(Key(obs.WiFi, "00:11:22:00:00:02")); !d.New {
		t.Errorf("van must be new: %+v", d)
	}
	al := e.Alerts(0)
	if len(al) != 1 || al[0].Kind != "new" || al[0].Key != Key(obs.WiFi, "00:11:22:00:00:02") {
		t.Fatalf("alerts: far and randomized radios must not alert; got %+v", al)
	}
	if n := len(e.List(Filter{NewOnly: true})); n != 3 {
		t.Errorf("new devices = %d, want 3", n)
	}
	if !e.Accept(Key(obs.WiFi, "00:11:22:00:00:02")) {
		t.Fatal("accept failed")
	}
	if err := e.Save(); err != nil {
		t.Fatal(err)
	}

	// A new engine with the same data directory knows the van at once.
	e2 := newEngine(t, dir)
	e2.Ingest(obs.Observation{At: at(time.Hour), Kind: obs.WiFi, MAC: "00:00:00:00:00:09", Frame: obs.FrameBeacon, RSSI: -50})
	e2.Ingest(obs.Observation{At: at(2 * time.Hour), Kind: obs.WiFi, MAC: "00:11:22:00:00:02", Frame: obs.FrameBeacon, RSSI: -50})
	if d, _ := e2.Get(Key(obs.WiFi, "00:11:22:00:00:02")); d.New {
		t.Error("van was accepted; it must not be new")
	}
}

func TestSignatureAlertAndClients(t *testing.T) {
	e := newEngine(t, "")
	// Learning time ends at once for this test.
	e.cfg.Learn = 0
	var attention *sig.Fleet
	for i, f := range e.m.Catalog().Fleets {
		if f.AttentionNote != "" && f.Rules[0].Kind == sig.NameGlob {
			attention = &e.m.Catalog().Fleets[i]
			break
		}
	}
	if attention == nil {
		t.Skip("no attention signature with a name glob in the stock catalog")
	}
	name := attention.Rules[0].Text
	name = strings.NewReplacer("*", "x", "?", "x").Replace(name)
	e.Ingest(obs.Observation{At: at(0), Kind: obs.BLE, MAC: "C0:00:00:00:00:01", Frame: obs.FrameAdv, Name: name, RSSI: -50})
	found := false
	for _, a := range e.Alerts(0) {
		if a.Kind == "signature" && a.Title == attention.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("no signature alert for %s (name %q): %+v", attention.ID, name, e.Alerts(0))
	}

	e.Ingest(obs.Observation{At: at(time.Second), Kind: obs.WiFi, MAC: "02:00:00:00:00:05", Frame: obs.FrameData, BSSID: "00:0F:B3:00:00:01"})
	if c := e.Clients("00:0F:B3:00:00:01"); len(c) != 1 {
		t.Errorf("clients: %+v", c)
	}
}

func TestDeauthBurst(t *testing.T) {
	e := newEngine(t, "")
	for i := range 25 {
		e.Ingest(obs.Observation{At: at(time.Duration(i) * 100 * time.Millisecond), Kind: obs.WiFi, MAC: "00:0F:B3:00:00:01", Frame: "deauth", BSSID: "00:0F:B3:00:00:01"})
	}
	n := 0
	for _, a := range e.Alerts(0) {
		if a.Kind == "deauth" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("deauth alerts = %d, want 1", n)
	}
}

func TestNonMACRadios(t *testing.T) {
	e := newEngine(t, "")
	e.cfg.Learn = 0
	e.Ingest(obs.Observation{At: at(0), Kind: obs.BLE, MAC: "uuid:6F1C2D3E-0000-4000-8000-000000000001", Frame: obs.FrameAdv, RSSI: -50})
	e.Ingest(obs.Observation{At: at(time.Second), Kind: obs.ISM, MAC: "Schrader/1A2B3C4", Name: "Schrader TPMS", RSSI: -9,
		Fields: map[string]string{"pressure_kPa": "231.5"}})
	b, _ := e.Get("ble/uuid:6F1C2D3E-0000-4000-8000-000000000001")
	if !b.Randomized || !b.New {
		t.Errorf("a macOS Bluetooth id counts as random: %+v", b)
	}
	s, _ := e.Get("ism/Schrader/1A2B3C4")
	if s.Role != RoleISM || s.Fields["pressure_kPa"] != "231.5" || len(s.Sigs) != 0 {
		t.Errorf("sub-GHz sensor: %+v", s)
	}
	al := e.Alerts(0)
	if len(al) != 1 || al[0].Title != "New sub-GHz sensor" {
		t.Errorf("want one alert, for the sensor only: %+v", al)
	}
}

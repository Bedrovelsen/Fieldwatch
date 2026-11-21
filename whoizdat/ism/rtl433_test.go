package ism

import (
	"strings"
	"testing"
	"time"

	"whoizdat/obs"
)

func TestParse(t *testing.T) {
	now := time.Unix(1, 0)
	o, err := Parse([]byte(`{"time" : "1763700000.250000", "model" : "Schrader", "type" : "TPMS", "flags" : "0", "id" : "1A2B3C4", "pressure_kPa" : 231.5, "temperature_C" : 12.0, "mic" : "CRC", "mod" : "ASK", "freq" : 315.012, "rssi" : -7.1, "snr" : 18.2, "noise" : -25.3}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != obs.ISM || o.MAC != "Schrader/1A2B3C4" || o.Name != "Schrader TPMS" || o.MHz != 315 || o.RSSI != -7 {
		t.Errorf("got %+v", o)
	}
	if !o.At.Equal(time.Unix(1763700000, 250000000)) {
		t.Errorf("time %v", o.At)
	}
	if o.Fields["pressure_kPa"] != "231.5" || o.Fields["temperature_C"] != "12.0" || o.Fields["mic"] != "" {
		t.Errorf("fields %v", o.Fields)
	}

	o, err = Parse([]byte(`{"time":"2026-11-21 09:00:00","model":"Acurite-Tower","id":4242,"channel":"A","battery_ok":1,"temperature_C":-3.4,"humidity":81}`), now)
	if err != nil || o.MAC != "Acurite-Tower/4242/chA" || o.RSSI != 0 || o.At.Hour() != 9 {
		t.Errorf("got %+v %v", o, err)
	}
	if _, err := Parse([]byte(`{"time":"x"}`), now); err == nil {
		t.Error("a line with no model must fail")
	}
}

func TestRead(t *testing.T) {
	in := "{\"model\":\"Toyota\",\"type\":\"TPMS\",\"id\":\"f1e2d3c4\"}\nnot json\n\n{\"model\":\"Toyota\",\"type\":\"TPMS\",\"id\":\"f1e2d3c5\"}\n"
	var ids []string
	bad, err := Read(strings.NewReader(in), func(o obs.Observation) bool { ids = append(ids, o.MAC); return true })
	if err != nil || bad != 1 || len(ids) != 2 || ids[0] != "Toyota/f1e2d3c4" {
		t.Errorf("bad=%d err=%v ids=%v", bad, err, ids)
	}
}

package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whoizdat/engine"
	"whoizdat/geo"
	"whoizdat/obs"
)

func TestMapJSON(t *testing.T) {
	e, h := testServer(t)
	t0 := time.Date(2026, 11, 21, 12, 3, 0, 0, time.UTC)
	e.Ingest(obs.Observation{At: t0, Kind: obs.ISM, MAC: "Schrader/1", Name: "Schrader TPMS", RSSI: -9})
	code, body := get(t, h, "/map.json?within=all", nil)
	if code != 200 {
		t.Fatalf("map.json: %d %s", code, body)
	}
	var d struct {
		Site    [2]float64
		Devices struct {
			Features []struct {
				Geometry   struct{ Coordinates [2]float64 }
				Properties map[string]any
			}
		}
		Rings      struct{ Features []any }
		NoDistance int `json:"no_distance"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	if d.Site != [2]float64{-75.6972, 45.4215} || len(d.Rings.Features) != len(ringsM) {
		t.Errorf("site %v rings %d", d.Site, len(d.Rings.Features))
	}
	// Three Wi-Fi radios have dBm levels; the sub-GHz sensor does not.
	if len(d.Devices.Features) != 3 || d.NoDistance != 1 {
		t.Fatalf("devices %d, no distance %d", len(d.Devices.Features), d.NoDistance)
	}
	site := geo.Point{Lat: 45.4215, Lon: -75.6972}
	for _, f := range d.Devices.Features {
		p := f.Properties
		near, est, far := p["near"].(float64), p["est"].(float64), p["far"].(float64)
		if !(near <= est && est <= far) {
			t.Errorf("%v: band %v %v %v", p["key"], near, est, far)
		}
		// The dot is at the estimated distance from the site.
		c := f.Geometry.Coordinates
		back := site.Offset(est, geo.Bearing(p["key"].(string)))
		if diff := (back.Lat-c[1])*(back.Lat-c[1]) + (back.Lon-c[0])*(back.Lon-c[0]); diff > 1e-10 {
			t.Errorf("%v: dot not at its distance", p["key"])
		}
	}
	if _, body := get(t, h, "/map", nil); !strings.Contains(body, `data-lat="45.4215"`) || !strings.Contains(body, "maplibre-gl-csp.js") {
		t.Errorf("map page:\n%.300s", body)
	}
	if _, body := get(t, h, "/", nil); strings.Contains(body, "maplibre") {
		t.Error("only the map page loads MapLibre")
	}
}

func TestMapNeedsSite(t *testing.T) {
	e, err := engine.New(engine.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := New(e, Options{})
	h := s.Handler()
	if code, _ := get(t, h, "/map.json", nil); code != 404 {
		t.Errorf("map.json with no site: %d", code)
	}
	if _, body := get(t, h, "/map", nil); !strings.Contains(body, "-site lat,lon") {
		t.Error("map page must explain -site")
	}
	if code, _ := get(t, h, "/map.pmtiles", nil); code != 404 {
		t.Errorf("no basemap: %d", code)
	}
}

func TestBasemapRange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "area.pmtiles")
	os.WriteFile(p, []byte("PMTiles\x03-0123456789abcdef"), 0o600)
	e, _ := engine.New(engine.Config{}, nil)
	s, _ := New(e, Options{Basemap: p})
	r := httptest.NewRequest("GET", "/map.pmtiles", nil)
	r.Host = "localhost"
	r.Header.Set("Range", "bytes=0-6")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "PMTiles" {
		t.Errorf("range: %d %q", w.Code, w.Body.String())
	}
}

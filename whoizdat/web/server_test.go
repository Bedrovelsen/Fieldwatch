package web

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"whoizdat/engine"
	"whoizdat/geo"
	"whoizdat/obs"
	"whoizdat/sig"
)

func testServer(t *testing.T) (*engine.Engine, http.Handler) {
	t.Helper()
	c, err := sig.Load("../../dist/fieldwatch-signatures.rsi")
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.Config{Learn: time.Minute, NearRSSI: -90}, sig.New(c))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	e.Ingest(obs.Observation{At: t0, Kind: obs.WiFi, MAC: "F8:BB:BF:10:20:30", Frame: obs.FrameBeacon, Name: "eero-home", RSSI: -40, Chan: 6})
	e.Ingest(obs.Observation{At: t0.Add(2 * time.Minute), Kind: obs.WiFi, MAC: "00:30:44:12:34:56", Frame: obs.FrameBeacon, Name: "Cradlepoint-IBR900", RSSI: -60})
	e.Ingest(obs.Observation{At: t0.Add(2 * time.Minute), Kind: obs.WiFi, MAC: "DA:A1:19:55:66:77", Frame: obs.FrameData, BSSID: "F8:BB:BF:10:20:30", RSSI: -50})
	s, err := New(e, Options{AllowHosts: []string{"box.tail1234.ts.net"}, Site: &geo.Point{Lat: 45.4215, Lon: -75.6972}})
	if err != nil {
		t.Fatal(err)
	}
	return e, s.Handler()
}

func get(t *testing.T, h http.Handler, target string, hdr map[string]string) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	r.Host = "127.0.0.1:8080"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(b)
}

func TestPages(t *testing.T) {
	_, h := testServer(t)
	for _, tc := range []struct{ path, want string }{
		{"/", "eero-home"},
		{"/alerts", "3 Wi-Fi, 0 Bluetooth and 0 sub-GHz frames"},
		{"/signatures", "<summary>ISP ("},
		{"/?new=1", "Cradlepoint-IBR900"},
		{"/devices?kind=wifi&role=client", "DA:A1:19:55:66:77"},
		{"/stats", "0 Bluetooth and 0 sub-GHz frames"},
		{"/device?key=" + url.QueryEscape("wifi/F8:BB:BF:10:20:30"), "Clients of this access point"},
		{"/device?key=" + url.QueryEscape("wifi/00:30:44:12:34:56"), "Add to home baseline"},
		{"/alerts", "New access point"},
		{"/signatures", "signatures in the catalog"},
		{"/static/fixi.js", "fx-action"},
	} {
		code, body := get(t, h, tc.path, nil)
		if code != 200 || !strings.Contains(body, tc.want) {
			t.Errorf("GET %s: %d, want %q in body:\n%.400s", tc.path, code, tc.want, body)
		}
	}
	if _, body := get(t, h, "/?new=1", nil); strings.Contains(body, "eero-home</a>") {
		t.Error("new=1 must hide the learned access point")
	}
	// Each link to a radio must open that radio.
	_, body := get(t, h, "/", nil)
	links := regexp.MustCompile(`href="(/device\?key=[^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(links) != 3 {
		t.Fatalf("radio links = %d, want 3", len(links))
	}
	for _, l := range links {
		if code, _ := get(t, h, html.UnescapeString(l[1]), nil); code != 200 {
			t.Errorf("GET %s: %d", l[1], code)
		}
	}
	if code, _ := get(t, h, "/device?key=nope", nil); code != 404 {
		t.Errorf("unknown radio: %d", code)
	}
	// A fixi request for alerts gets only the partial.
	if _, body := get(t, h, "/alerts", map[string]string{"FX-Request": "true"}); strings.Contains(body, "<html") {
		t.Error("fixi alerts request must get a partial")
	}
}

func TestGuard(t *testing.T) {
	e, h := testServer(t)
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "evil.example:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("unknown host: %d", w.Code)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Host = "box.tail1234.ts.net"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("allowed host: %d, CSP %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}

	post := func(origin, site string) int {
		r := httptest.NewRequest("POST", "/accept", strings.NewReader("key="+url.QueryEscape("wifi/00:30:44:12:34:56")))
		r.Host = "127.0.0.1:8080"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("FX-Request", "true")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := post("http://evil.example", "cross-site"); c != 403 {
		t.Errorf("cross-site POST: %d", c)
	}
	if c := post("http://evil.example", ""); c != 403 {
		t.Errorf("cross-origin POST: %d", c)
	}
	if c := post("http://127.0.0.1:8080", "same-origin"); c != 200 {
		t.Errorf("same-origin POST: %d", c)
	}
	if d, _ := e.Get("wifi/00:30:44:12:34:56"); d.New {
		t.Error("accept did not add the radio to the baseline")
	}
}

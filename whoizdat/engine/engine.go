// Package engine keeps the table of radios, the home baseline, and alerts.
//
// The engine has no goroutines of its own. The caller sends observations
// with Ingest and reads views with List, Get, Alerts and Stats. The clock is
// the time of the newest observation, so a replay of old data gives the same
// result as live data.
package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"whoizdat/obs"
	"whoizdat/sig"
)

// Roles.
const (
	RoleAP     = "ap"
	RoleClient = "client"
	RoleBLE    = "ble"
)

// Device is everything known about one transmitter.
type Device struct {
	Key        string
	Kind       obs.Kind
	MAC        string
	Role       string
	Name       string   // SSID or BLE name, newest non-empty
	Probes     []string // networks a client asked for
	BSSID      string   // access point a client talks to
	Hidden     bool
	Randomized bool
	Chan, MHz  int
	RSSI       int // newest
	RSSIMax    int
	First      time.Time
	Last       time.Time
	Count      int
	Deauths    int

	VendorIEs   []string
	UUIDs       []string
	Mfg         []obs.Mfg
	ServiceData []obs.ServiceData

	Sigs  []string // matched signature ids
	New   bool     // not in the home baseline
	dirty bool     // identity changed, match again
}

// Alert is one event for the operator.
type Alert struct {
	At    time.Time
	Kind  string // "new", "signature", "deauth"
	Key   string
	Title string
	Text  string
}

type Config struct {
	DataDir   string        // known.json goes here; "" = no file
	Learn     time.Duration // after the first observation, every radio heard in this time is home
	NearRSSI  int           // only radios at this level or stronger raise "new" alerts
	AlertRand bool          // also raise "new" alerts for randomized addresses
	Keep      time.Duration // forget radios not heard for this long (baseline stays)
}

type Engine struct {
	mu      sync.RWMutex
	cfg     Config
	m       *sig.Matcher
	devs    map[string]*Device
	known   map[string]time.Time
	kdirty  bool
	alerts  []Alert
	start   time.Time
	now     time.Time
	frames  map[obs.Kind]int
	deauths []time.Time // last minute
	lastDA  time.Time
	pruned  time.Time
}

const maxAlerts = 500

func New(cfg Config, m *sig.Matcher) (*Engine, error) {
	if cfg.Keep == 0 {
		cfg.Keep = 24 * time.Hour
	}
	e := &Engine{cfg: cfg, m: m, devs: map[string]*Device{}, known: map[string]time.Time{}, frames: map[obs.Kind]int{}}
	if cfg.DataDir != "" {
		b, err := os.ReadFile(filepath.Join(cfg.DataDir, "known.json"))
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &e.known); err != nil {
				return nil, fmt.Errorf("known.json: %w", err)
			}
		case !os.IsNotExist(err):
			return nil, err
		}
	}
	return e, nil
}

func Key(k obs.Kind, mac string) string { return string(k) + "/" + mac }

// Ingest adds one observation.
func (e *Engine) Ingest(o obs.Observation) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.start.IsZero() {
		e.start = o.At
	}
	if o.At.After(e.now) {
		e.now = o.At
	}
	e.frames[o.Kind]++
	if o.Frame == "deauth" {
		e.deauthSeen(o)
	}
	k := Key(o.Kind, o.MAC)
	d := e.devs[k]
	if d == nil {
		d = &Device{Key: k, Kind: o.Kind, MAC: o.MAC, First: o.At, RSSIMax: -200, Randomized: obs.Randomized(o.MAC), dirty: true}
		e.devs[k] = d
		e.baseline(d, o)
	}
	update(d, o)
	if d.dirty && e.m != nil {
		d.dirty = false
		before := d.Sigs
		d.Sigs = e.m.Match(sig.Radio{Kind: d.Kind, MAC: d.MAC, Name: d.Name, Hidden: d.Hidden,
			UUIDs: d.UUIDs, Mfg: d.Mfg, ServiceData: d.ServiceData, VendorIEs: d.VendorIEs})
		for _, id := range d.Sigs {
			if slices.Contains(before, id) {
				continue
			}
			if f := e.m.Catalog().ByID(id); f != nil && f.AttentionNote != "" {
				e.alert(Alert{At: o.At, Kind: "signature", Key: k, Title: f.Name, Text: f.AttentionNote})
			}
		}
	}
	if e.now.Sub(e.pruned) > time.Minute {
		e.prune()
	}
}

func (e *Engine) baseline(d *Device, o obs.Observation) {
	if _, ok := e.known[d.Key]; ok {
		return
	}
	if o.At.Sub(e.start) < e.cfg.Learn {
		e.known[d.Key] = o.At
		e.kdirty = true
		return
	}
	d.New = true
	if d.Randomized && !e.cfg.AlertRand {
		return
	}
	if o.RSSI != 0 && o.RSSI < e.cfg.NearRSSI {
		return
	}
	e.alert(Alert{At: o.At, Kind: "new", Key: d.Key, Title: "New " + roleName(o), Text: o.MAC + " " + o.Name})
}

func roleName(o obs.Observation) string {
	switch {
	case o.Kind == obs.BLE:
		return "Bluetooth device"
	case o.Frame == obs.FrameBeacon || o.Frame == obs.FrameProbeResp:
		return "access point"
	}
	return "Wi-Fi client"
}

func (e *Engine) deauthSeen(o obs.Observation) {
	cut := o.At.Add(-10 * time.Second)
	i := 0
	for i < len(e.deauths) && e.deauths[i].Before(cut) {
		i++
	}
	e.deauths = append(e.deauths[i:], o.At)
	if len(e.deauths) >= 20 && o.At.Sub(e.lastDA) > time.Minute {
		e.lastDA = o.At
		e.alert(Alert{At: o.At, Kind: "deauth", Key: Key(o.Kind, o.MAC), Title: "Deauthentication burst",
			Text: fmt.Sprintf("%d deauthentication frames in 10 s. Last from %s for network %s. The sender address can be false.", len(e.deauths), o.MAC, o.BSSID)})
	}
}

func update(d *Device, o obs.Observation) {
	d.Count++
	if o.At.After(d.Last) {
		d.Last = o.At
	}
	if o.At.Before(d.First) {
		d.First = o.At
	}
	if o.RSSI != 0 {
		d.RSSI = o.RSSI
		d.RSSIMax = max(d.RSSIMax, o.RSSI)
	}
	if o.Chan != 0 {
		d.Chan, d.MHz = o.Chan, o.MHz
	}
	switch {
	case o.Kind == obs.BLE:
		d.Role = RoleBLE
	case o.Frame == obs.FrameBeacon || o.Frame == obs.FrameProbeResp:
		d.Role = RoleAP
	case d.Role == "":
		d.Role = RoleClient
	}
	switch o.Frame {
	case obs.FrameProbeReq:
		if o.Name != "" && !slices.Contains(d.Probes, o.Name) && len(d.Probes) < 64 {
			d.Probes = append(d.Probes, o.Name)
		}
	case obs.FrameData:
		d.BSSID = o.BSSID
	case "deauth":
		d.Deauths++
	default:
		if o.Name != "" && o.Name != d.Name {
			d.Name, d.dirty = o.Name, true
		}
		if o.Hidden != d.Hidden && o.Frame == obs.FrameBeacon {
			d.Hidden, d.dirty = o.Hidden, true
		}
	}
	d.VendorIEs = addAll(d.VendorIEs, o.VendorIEs, &d.dirty)
	d.UUIDs = addAll(d.UUIDs, o.UUIDs, &d.dirty)
	for _, m := range o.Mfg {
		if !slices.Contains(d.Mfg, m) && len(d.Mfg) < 16 {
			d.Mfg, d.dirty = append(d.Mfg, m), true
		}
	}
	for _, s := range o.ServiceData {
		if !slices.Contains(d.ServiceData, s) && len(d.ServiceData) < 16 {
			d.ServiceData, d.dirty = append(d.ServiceData, s), true
		}
	}
}

func addAll(dst, src []string, dirty *bool) []string {
	for _, s := range src {
		if !slices.Contains(dst, s) && len(dst) < 32 {
			dst, *dirty = append(dst, s), true
		}
	}
	return dst
}

func (e *Engine) alert(a Alert) {
	e.alerts = append(e.alerts, a)
	if len(e.alerts) > maxAlerts {
		e.alerts = slices.Delete(e.alerts, 0, len(e.alerts)-maxAlerts)
	}
}

func (e *Engine) prune() {
	e.pruned = e.now
	for k, d := range e.devs {
		if e.now.Sub(d.Last) > e.cfg.Keep {
			delete(e.devs, k)
		}
	}
}

// Filter selects devices for List.
type Filter struct {
	Kind    obs.Kind // "" = all
	Role    string   // "" = all
	NewOnly bool
	SigOnly bool
	Query   string // in MAC, name, probes or signature id
	MinRSSI int    // 0 = any
	Within  time.Duration
	Sort    string // "last" (default), "rssi", "first", "count"
	Limit   int
}

// List returns copies of the devices that pass f.
func (e *Engine) List(f Filter) []Device {
	e.mu.RLock()
	defer e.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(f.Query))
	var out []Device
	for _, d := range e.devs {
		switch {
		case f.Kind != "" && d.Kind != f.Kind,
			f.Role != "" && d.Role != f.Role,
			f.NewOnly && !d.New,
			f.SigOnly && len(d.Sigs) == 0,
			f.MinRSSI != 0 && (d.RSSI == 0 || d.RSSI < f.MinRSSI),
			f.Within > 0 && e.now.Sub(d.Last) > f.Within,
			q != "" && !matchesQuery(d, q):
			continue
		}
		out = append(out, copyDev(d))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch f.Sort {
		case "rssi":
			if a.RSSI != b.RSSI {
				return a.RSSI > b.RSSI
			}
		case "first":
			if !a.First.Equal(b.First) {
				return a.First.After(b.First)
			}
		case "count":
			if a.Count != b.Count {
				return a.Count > b.Count
			}
		}
		if !a.Last.Equal(b.Last) {
			return a.Last.After(b.Last)
		}
		return a.Key < b.Key
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

func matchesQuery(d *Device, q string) bool {
	if strings.Contains(strings.ToLower(d.MAC), q) || strings.Contains(strings.ToLower(d.Name), q) {
		return true
	}
	for _, s := range append(slices.Clone(d.Probes), d.Sigs...) {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

func copyDev(d *Device) Device {
	c := *d
	c.Probes = slices.Clone(d.Probes)
	c.Sigs = slices.Clone(d.Sigs)
	c.VendorIEs = slices.Clone(d.VendorIEs)
	c.UUIDs = slices.Clone(d.UUIDs)
	c.Mfg = slices.Clone(d.Mfg)
	c.ServiceData = slices.Clone(d.ServiceData)
	return c
}

func (e *Engine) Get(key string) (Device, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	d, ok := e.devs[key]
	if !ok {
		return Device{}, false
	}
	return copyDev(d), true
}

// Clients returns the clients that talk to the access point with this MAC.
func (e *Engine) Clients(bssid string) []Device {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Device
	for _, d := range e.devs {
		if d.Role == RoleClient && d.BSSID == bssid {
			out = append(out, copyDev(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// Alerts returns the newest alerts first.
func (e *Engine) Alerts(limit int) []Alert {
	e.mu.RLock()
	defer e.mu.RUnlock()
	n := len(e.alerts)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]Alert, 0, n)
	for i := len(e.alerts) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, e.alerts[i])
	}
	return out
}

type Stats struct {
	Now       time.Time
	Learning  bool
	LearnLeft time.Duration
	Devices   int
	New       int
	Known     int
	Frames    map[string]int
	Alerts    int
}

func (e *Engine) Stats() Stats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := Stats{Now: e.now, Devices: len(e.devs), Known: len(e.known), Alerts: len(e.alerts), Frames: map[string]int{}}
	for k, v := range e.frames {
		s.Frames[string(k)] = v
	}
	for _, d := range e.devs {
		if d.New {
			s.New++
		}
	}
	if !e.start.IsZero() {
		left := e.cfg.Learn - e.now.Sub(e.start)
		s.Learning, s.LearnLeft = left > 0, max(left, 0)
	}
	return s
}

// Accept adds a device to the home baseline.
func (e *Engine) Accept(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.devs[key]
	if !ok {
		return false
	}
	d.New = false
	e.known[key] = d.First
	e.kdirty = true
	return true
}

// AcceptAll adds every device now in the table to the home baseline.
func (e *Engine) AcceptAll() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for k, d := range e.devs {
		if d.New {
			d.New = false
			e.known[k] = d.First
			n++
		}
	}
	e.kdirty = e.kdirty || n > 0
	return n
}

// Save writes the home baseline if it changed.
func (e *Engine) Save() error {
	e.mu.Lock()
	if !e.kdirty || e.cfg.DataDir == "" {
		e.mu.Unlock()
		return nil
	}
	b, err := json.MarshalIndent(e.known, "", " ")
	e.kdirty = false
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.cfg.DataDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(e.cfg.DataDir, "known.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Signature returns a catalog entry, or nil.
func (e *Engine) Signature(id string) *sig.Fleet {
	if e.m == nil {
		return nil
	}
	return e.m.Catalog().ByID(id)
}

// Catalog returns the signature catalog, or nil.
func (e *Engine) Catalog() *sig.Catalog {
	if e.m == nil {
		return nil
	}
	return e.m.Catalog()
}

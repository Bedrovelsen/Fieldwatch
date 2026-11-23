package web

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"whoizdat/engine"
	"whoizdat/geo"
	"whoizdat/obs"
)

// Default distance models. The 1 m levels are typical for a home access point
// and a phone; walls make n larger than in open air.
var (
	DefaultWiFi = geo.Model{Ref: -40, N: 2.7, NNear: 3.5, NFar: 2.0, MaxM: 400}
	DefaultBLE  = geo.Model{Ref: -59, N: 2.7, NNear: 3.5, NFar: 2.0, MaxM: 200}
)

var ringsM = []float64{10, 25, 50, 100, 200}

type mapView struct {
	Title   string
	Nav     string
	Site    *geo.Point
	Basemap bool
	Query   string
	F       engine.Filter
	Within  string
}

func (s *Server) mapPage(w http.ResponseWriter, r *http.Request) {
	l := s.listFrom(r)
	s.render(w, "map", mapView{Title: "Map", Nav: "map", Site: s.opt.Site, Basemap: s.opt.Basemap != "",
		Query: l.Query, F: l.F, Within: l.Within})
}

type feature struct {
	Type       string         `json:"type"`
	Geometry   geometry       `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type geometry struct {
	Type        string `json:"type"`
	Coordinates any    `json:"coordinates"`
}

type collection struct {
	Type     string    `json:"type"`
	Features []feature `json:"features"`
}

type mapData struct {
	Site       [2]float64 `json:"site"`
	Devices    collection `json:"devices"`
	Rings      collection `json:"rings"`
	NoDistance int        `json:"no_distance"` // radios with no dBm level (sub-GHz, or no level)
}

func (s *Server) mapJSON(w http.ResponseWriter, r *http.Request) {
	if s.opt.Site == nil {
		http.Error(w, "no site position; start serve with -site lat,lon", http.StatusNotFound)
		return
	}
	site := *s.opt.Site
	l := s.listFrom(r)
	now := s.e.Stats().Now
	d := mapData{Site: [2]float64{site.Lon, site.Lat},
		Devices: collection{Type: "FeatureCollection", Features: []feature{}},
		Rings:   collection{Type: "FeatureCollection", Features: []feature{}}}
	for _, m := range ringsM {
		d.Rings.Features = append(d.Rings.Features, feature{Type: "Feature",
			Geometry:   geometry{"LineString", site.Circle(m, 72)},
			Properties: map[string]any{"m": m}})
	}
	for _, dev := range l.Devices {
		model, ok := s.model(dev)
		if !ok {
			d.NoDistance++
			continue
		}
		near, est, far := model.Range(dev.RSSIAvg)
		p := site.Offset(est, geo.Bearing(dev.Key))
		var sigs []string
		for _, id := range dev.Sigs {
			if f := s.e.Signature(id); f != nil {
				sigs = append(sigs, f.Name)
			}
		}
		name := dev.Name
		if name == "" && len(dev.Probes) > 0 {
			name = "asks for " + dev.Probes[0]
		}
		d.Devices.Features = append(d.Devices.Features, feature{Type: "Feature",
			Geometry: geometry{"Point", [2]float64{p.Lon, p.Lat}},
			Properties: map[string]any{
				"key": dev.Key, "name": name, "mac": dev.MAC, "role": dev.Role,
				"new": dev.New, "attention": s.attention(dev.Sigs), "sigs": strings.Join(sigs, ", "),
				"rssi": math.Round(dev.RSSIAvg), "near": math.Round(near), "est": math.Round(est), "far": math.Round(far),
				"ago": int(now.Sub(dev.Last) / time.Second),
			}})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(d)
}

// model returns the distance model for a radio, or false when its level is not in dBm.
func (s *Server) model(d engine.Device) (geo.Model, bool) {
	if d.RSSIAvg == 0 {
		return geo.Model{}, false
	}
	switch d.Kind {
	case obs.WiFi:
		return s.opt.WiFi, true
	case obs.BLE:
		return s.opt.BLE, true
	}
	return geo.Model{}, false
}

func (s *Server) attention(ids []string) bool {
	for _, id := range ids {
		if f := s.e.Signature(id); f != nil && f.AttentionNote != "" {
			return true
		}
	}
	return false
}

// basemap serves the .pmtiles file. http.ServeContent answers the HTTP range
// requests that PMTiles readers send.
func (s *Server) basemap(w http.ResponseWriter, r *http.Request) {
	if s.opt.Basemap == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.opt.Basemap)
	if err != nil {
		http.Error(w, "basemap: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "basemap: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.pmtiles")
	http.ServeContent(w, r, "map.pmtiles", st.ModTime(), f)
}

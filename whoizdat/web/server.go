// Package web serves the user interface. Pages are html/template
// compositions: one layout, one file per page, and partials that fixi
// requests to swap one part of a page. Every page also works without
// JavaScript, as plain links and GET forms.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"whoizdat/engine"
	"whoizdat/geo"
	"whoizdat/obs"
	"whoizdat/sig"
)

//go:embed tmpl static
var files embed.FS

// Options for New. A zero Options is valid: localhost only, no map.
type Options struct {
	AllowHosts []string   // extra Host names that may reach the server
	Site       *geo.Point // sensor position; nil = no map
	Basemap    string     // .pmtiles file for the map; "" = no basemap
	WiFi, BLE  geo.Model  // distance models
}

type Server struct {
	opt     Options
	e       *engine.Engine
	base    *template.Template
	pages   map[string]*template.Template
	allowed []string // Host header values that are accepted
}

// New makes a server. allowHosts are extra host names (without port) that
// may reach it, for example a Tailscale name.
func New(e *engine.Engine, opt Options) (*Server, error) {
	if opt.WiFi.N == 0 {
		opt.WiFi = DefaultWiFi
	}
	if opt.BLE.N == 0 {
		opt.BLE = DefaultBLE
	}
	s := &Server{e: e, opt: opt, pages: map[string]*template.Template{},
		allowed: append([]string{"localhost", "127.0.0.1", "::1"}, opt.AllowHosts...)}
	base, err := template.New("").Funcs(s.funcs()).ParseFS(files, "tmpl/layout.html", "tmpl/part_*.html")
	if err != nil {
		return nil, err
	}
	s.base = base
	pages, _ := fs.Glob(files, "tmpl/page_*.html")
	for _, p := range pages {
		t, err := template.Must(base.Clone()).ParseFS(files, p)
		if err != nil {
			return nil, err
		}
		s.pages[strings.TrimSuffix(strings.TrimPrefix(p, "tmpl/page_"), ".html")] = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.live)
	mux.HandleFunc("GET /devices", s.devices)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /device", s.device)
	mux.HandleFunc("GET /alerts", s.alerts)
	mux.HandleFunc("GET /signatures", s.signatures)
	mux.HandleFunc("GET /map", s.mapPage)
	mux.HandleFunc("GET /map.json", s.mapJSON)
	mux.HandleFunc("GET /map.pmtiles", s.basemap)
	mux.HandleFunc("POST /accept", s.accept)
	mux.HandleFunc("POST /accept-all", s.acceptAll)
	return s.guard(mux)
}

// guard rejects unknown Host headers (DNS rebinding) and cross-site POSTs.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !slices.Contains(s.allowed, strings.Trim(host, "[]")) {
			http.Error(w, "host not allowed; start with -allow-host "+host, http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "cross-site request", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request", http.StatusForbidden)
					return
				}
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"ago": func(t time.Time) string {
			d := s.e.Stats().Now.Sub(t)
			switch {
			case d < 2*time.Second:
				return "now"
			case d < time.Minute:
				return fmt.Sprintf("%d s", int(d.Seconds()))
			case d < time.Hour:
				return fmt.Sprintf("%d min", int(d.Minutes()))
			case d < 48*time.Hour:
				return fmt.Sprintf("%d h", int(d.Hours()))
			}
			return fmt.Sprintf("%d d", int(d.Hours()/24))
		},
		"stamp": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") },
		"dur":   func(d time.Duration) string { return d.Round(time.Second).String() },
		"sig":   func(id string) *sig.Fleet { return s.e.Signature(id) },
		"stats": func() engine.Stats { return s.e.Stats() },
		"title": func(s string) string {
			if s == "ISP" || s == "" {
				return s
			}
			s = strings.ReplaceAll(strings.ToLower(s), "_", " ")
			if s == "" {
				return s
			}
			return strings.ToUpper(s[:1]) + s[1:]
		},
		"role": func(r string) string {
			return map[string]string{engine.RoleAP: "Access point", engine.RoleClient: "Wi-Fi client", engine.RoleBLE: "Bluetooth", engine.RoleISM: "Sub-GHz sensor"}[r]
		},
		"hasAttention": func(ids []string) bool {
			for _, id := range ids {
				if f := s.e.Signature(id); f != nil && f.AttentionNote != "" {
					return true
				}
			}
			return false
		},
		"join":  strings.Join,
		"list":  func(v ...string) []string { return v },
		"list1": func(d *engine.Device) []engine.Device { return []engine.Device{*d} },
		"esc":   url.QueryEscape,
	}
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "no page "+page, http.StatusInternalServerError)
		return
	}
	write(w, t, "layout", data)
}

func (s *Server) part(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Cache-Control", "no-store")
	write(w, s.base, name, data)
}

// write renders to a buffer first, so a template error gives a 500 and not half a page.
func write(w http.ResponseWriter, t *template.Template, name string, data any) {
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		log.Printf("web: %s: %v", name, err)
		http.Error(w, "page error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b.Bytes())
}

// listView is the data for the live list and its filter form.
type listView struct {
	Title   string
	Nav     string
	F       engine.Filter
	Within  string
	Query   string // the filter as a query string, for polling
	Devices []engine.Device
	Stats   engine.Stats
}

func (s *Server) listFrom(r *http.Request) listView {
	q := r.URL.Query()
	f := engine.Filter{
		Kind:    obs.Kind(q.Get("kind")),
		Role:    q.Get("role"),
		NewOnly: q.Get("new") == "1",
		SigOnly: q.Get("sig") == "1",
		Query:   q.Get("q"),
		Sort:    q.Get("sort"),
		Limit:   300,
	}
	f.MinRSSI, _ = strconv.Atoi(q.Get("rssi"))
	within := q.Get("within")
	if within == "" {
		within = "10m"
	}
	if d, err := time.ParseDuration(within); err == nil && within != "all" {
		f.Within = d
	}
	keep := url.Values{}
	for _, k := range []string{"kind", "role", "new", "sig", "q", "sort", "rssi", "within"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	return listView{Title: "Live", Nav: "live", F: f, Within: within, Query: keep.Encode(),
		Devices: s.e.List(f), Stats: s.e.Stats()}
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	s.render(w, "live", s.listFrom(r))
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	s.part(w, "devices", s.listFrom(r))
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	s.part(w, "stats", s.e.Stats())
}

type deviceView struct {
	Title   string
	Nav     string
	D       engine.Device
	Clients []engine.Device
	AP      *engine.Device
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	d, ok := s.e.Get(r.URL.Query().Get("key"))
	if !ok {
		http.Error(w, "this radio is not in the table", http.StatusNotFound)
		return
	}
	v := deviceView{Title: d.MAC, Nav: "live", D: d}
	if d.Role == engine.RoleAP {
		v.Clients = s.e.Clients(d.MAC)
	}
	if d.BSSID != "" {
		if ap, ok := s.e.Get(engine.Key(obs.WiFi, d.BSSID)); ok {
			v.AP = &ap
		}
	}
	s.render(w, "device", v)
}

type alertView struct {
	Title  string
	Nav    string
	Alerts []engine.Alert
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	v := alertView{Title: "Alerts", Nav: "alerts", Alerts: s.e.Alerts(200)}
	if r.Header.Get("FX-Request") == "true" {
		s.part(w, "alerts", v)
		return
	}
	s.render(w, "alerts", v)
}

type sigGroup struct {
	Class  string
	Fleets []sig.Fleet
}

type sigView struct {
	Title  string
	Nav    string
	Groups []sigGroup
	Total  int
}

func (s *Server) signatures(w http.ResponseWriter, r *http.Request) {
	v := sigView{Title: "Signatures", Nav: "signatures"}
	byClass := map[string][]sig.Fleet{}
	for _, f := range s.catalog() {
		byClass[f.Class] = append(byClass[f.Class], f)
	}
	for c, fs := range byClass {
		v.Groups = append(v.Groups, sigGroup{c, fs})
		v.Total += len(fs)
	}
	slices.SortFunc(v.Groups, func(a, b sigGroup) int { return strings.Compare(a.Class, b.Class) })
	s.render(w, "signatures", v)
}

func (s *Server) catalog() []sig.Fleet {
	if c := s.e.Catalog(); c != nil {
		return c.Fleets
	}
	return nil
}

func (s *Server) accept(w http.ResponseWriter, r *http.Request) {
	key := r.FormValue("key")
	if !s.e.Accept(key) {
		http.Error(w, "this radio is not in the table", http.StatusNotFound)
		return
	}
	if r.Header.Get("FX-Request") == "true" {
		s.part(w, "accepted", nil)
		return
	}
	http.Redirect(w, r, "/device?key="+url.QueryEscape(key), http.StatusSeeOther)
}

func (s *Server) acceptAll(w http.ResponseWriter, r *http.Request) {
	n := s.e.AcceptAll()
	if r.Header.Get("FX-Request") == "true" {
		s.part(w, "accepted-all", n)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

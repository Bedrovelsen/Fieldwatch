// Command whoizdat is a passive radio awareness tool.
//
// It has three parts that connect only through JSONL on pipes:
//
//	whoizdat wifi   capture 802.11 frames and write observations (needs capture rights)
//	whoizdat demo   write test observations
//	whoizdat serve  read observations, match signatures, serve the web UI (no rights)
//
// Example on one Linux computer:
//
//	whoizdat wifi -iface wlan1 -hop 1-13 | whoizdat serve -catalog sigs.rsi -
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"whoizdat/engine"
	"whoizdat/geo"
	"whoizdat/ism"
	"whoizdat/obs"
	"whoizdat/sig"
	"whoizdat/web"
	"whoizdat/wifi"
)

const usage = `whoizdat: passive Wi-Fi and Bluetooth awareness.

Commands:
  wifi   capture Wi-Fi frames and write JSONL observations to stdout
  rtl433 read rtl_433 JSON on stdin and write observations to stdout
  demo   write test observations to stdout
  serve  read JSONL observations and serve the web UI

Run "whoizdat <command> -h" for the flags of a command.
`

func main() {
	log.SetFlags(log.Ltime)
	log.SetPrefix("whoizdat ")
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "wifi":
		err = cmdWifi(ctx, os.Args[2:])
	case "demo":
		err = cmdDemo(ctx, os.Args[2:])
	case "serve":
		err = cmdServe(ctx, os.Args[2:])
	case "rtl433":
		err = cmdRtl433(os.Args[2:])
	case "-h", "-help", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func cmdWifi(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("wifi", flag.ExitOnError)
	iface := fs.String("iface", "", "monitor-mode interface to capture from (Linux)")
	pcap := fs.String("pcap", "", `pcap or pcapng file to read, or "-" for stdin`)
	monitor := fs.Bool("monitor", false, "put -iface into monitor mode first (needs CAP_NET_ADMIN)")
	hop := fs.String("hop", "1-11,36-48,149-165", `channels to visit (default: Canada, no DFS); "" = stay`)
	dwell := fs.Duration("dwell", 300*time.Millisecond, "time on each channel")
	sensor := fs.String("sensor", "", "sensor name to put in each observation")
	fs.Parse(args)

	w := obs.NewWriter(os.Stdout)
	var mu sync.Mutex
	n := 0
	emit := func(p wifi.Packet) bool {
		o, ok := wifi.Decode(p)
		if !ok {
			return true
		}
		o.Sensor = *sensor
		mu.Lock()
		defer mu.Unlock()
		n++
		return w.Write(o) == nil // stop when the reader goes away
	}
	switch {
	case *pcap != "":
		var r io.Reader = os.Stdin
		if *pcap != "-" {
			f, err := os.Open(*pcap)
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		err := wifi.ReadCapture(r, emit)
		log.Printf("wifi: %d observations", n)
		return err
	case *iface != "":
		if *monitor {
			if err := wifi.Monitor(*iface); err != nil {
				return err
			}
		}
		chans, err := wifi.ParseChannels(*hop)
		if err != nil {
			return err
		}
		go wifi.Hop(ctx, *iface, chans, *dwell)
		log.Printf("wifi: capture on %s, channels %v", *iface, chans)
		return wifi.Live(ctx, *iface, emit)
	}
	return errors.New("wifi: give -iface or -pcap")
}

func cmdRtl433(args []string) error {
	fs := flag.NewFlagSet("rtl433", flag.ExitOnError)
	sensor := fs.String("sensor", "", "sensor name to put in each observation")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: rtl_433 -F json -M level ... | whoizdat rtl433 [flags]")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	w := obs.NewWriter(os.Stdout)
	bad, err := ism.Read(os.Stdin, func(o obs.Observation) bool {
		o.Sensor = *sensor
		return w.Write(o) == nil
	})
	if bad > 0 {
		log.Printf("rtl433: skipped %d lines that are not rtl_433 JSON", bad)
	}
	return err
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "address for the web UI")
	catalog := fs.String("catalog", "", "radio-signatures (.rsi) catalog file")
	data := fs.String("data", defaultDataDir(), "directory for the home baseline (known.json)")
	learn := fs.Duration("learn", 30*time.Minute, "radios heard in this time after the first observation are home")
	near := fs.Int("near", -80, "only radios at this dBm or stronger raise new-radio alerts")
	alertRand := fs.Bool("alert-random", false, "also raise new-radio alerts for random addresses")
	site := fs.String("site", "", `sensor position "lat,lon" for the map`)
	basemap := fs.String("basemap", "", "offline basemap .pmtiles file for the map")
	refWifi := fs.Float64("ref-wifi", web.DefaultWiFi.Ref, "Wi-Fi level (dBm) at 1 m, for distance")
	refBLE := fs.Float64("ref-ble", web.DefaultBLE.Ref, "Bluetooth level (dBm) at 1 m, for distance")
	pathN := fs.Float64("path-n", web.DefaultWiFi.N, "path-loss exponent: 2 = open air, 3 to 4 = through walls")
	var allow multi
	fs.Var(&allow, "allow-host", "extra host name that may reach the UI, for example a Tailscale name (repeat)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: whoizdat serve [flags] [input.jsonl ...]   (\"-\" or nothing = stdin)")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	var m *sig.Matcher
	if *catalog != "" {
		c, err := sig.Load(*catalog)
		if err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
		m = sig.New(c)
		log.Printf("serve: %d signatures (catalog %d)", len(c.Fleets), c.CatalogVersion)
	} else {
		log.Printf("serve: no -catalog, so no signature matches")
	}
	e, err := engine.New(engine.Config{DataDir: *data, Learn: *learn, NearRSSI: *near, AlertRand: *alertRand}, m)
	if err != nil {
		return err
	}
	opt := web.Options{AllowHosts: allow, Basemap: *basemap, WiFi: web.DefaultWiFi, BLE: web.DefaultBLE}
	opt.WiFi.Ref, opt.BLE.Ref = *refWifi, *refBLE
	opt.WiFi.N, opt.BLE.N = *pathN, *pathN
	if *site != "" {
		p, err := geo.ParsePoint(*site)
		if err != nil {
			return fmt.Errorf("-site: %w", err)
		}
		opt.Site = &p
	}
	if *basemap != "" {
		if _, err := os.Stat(*basemap); err != nil {
			return fmt.Errorf("-basemap: %w", err)
		}
	}
	srv, err := web.New(e, opt)
	if err != nil {
		return err
	}

	inputs := fs.Args()
	if len(inputs) == 0 {
		inputs = []string{"-"}
	}
	for _, in := range inputs {
		go func() {
			if err := readInput(in, e); err != nil {
				log.Printf("serve: input %s: %v", in, err)
			}
		}()
	}
	go func() {
		for t := time.NewTicker(30 * time.Second); ; {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := e.Save(); err != nil {
					log.Printf("serve: save: %v", err)
				}
			}
		}
	}()

	hs := &http.Server{Addr: *listen, Handler: srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	log.Printf("serve: open http://%s/  (data in %s)", *listen, *data)
	err = hs.ListenAndServe()
	if serr := e.Save(); serr != nil {
		log.Printf("serve: save: %v", serr)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func readInput(name string, e *engine.Engine) error {
	var r io.Reader = os.Stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	bad, err := obs.Read(r, func(o obs.Observation) bool { e.Ingest(o); return true })
	if bad > 0 {
		log.Printf("serve: input %s: skipped %d bad lines", name, bad)
	}
	log.Printf("serve: input %s ended", name)
	return err
}

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "whoizdat")
	}
	return "whoizdat-data"
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

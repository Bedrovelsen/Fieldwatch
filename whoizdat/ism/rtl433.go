// Package ism reads rtl_433 JSON output and makes observations.
//
// rtl_433 decodes hundreds of sub-GHz devices: tire pressure sensors (TPMS),
// weather stations, door and motion sensors, key fobs. It can read a LimeSDR
// through SoapySDR. Run it with -F json and, for signal levels, -M level:
//
//	rtl_433 -d driver=lime -f 315M -f 433.92M -H 30 -M level -F json | whoizdat rtl433
package ism

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"whoizdat/obs"
)

// Keys that are not device data.
var meta = map[string]bool{
	"time": true, "model": true, "id": true, "channel": true, "rssi": true, "snr": true,
	"noise": true, "freq": true, "freq1": true, "freq2": true, "mic": true, "mod": true,
}

// Parse makes an observation from one rtl_433 JSON object.
// now is used when the line has no time that can be read.
func Parse(line []byte, now time.Time) (obs.Observation, error) {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(line)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return obs.Observation{}, err
	}
	model := str(m["model"])
	if model == "" {
		return obs.Observation{}, fmt.Errorf("no model")
	}
	id := model
	if v, ok := m["id"]; ok {
		id += "/" + str(v)
	}
	if v, ok := m["channel"]; ok {
		id += "/ch" + str(v)
	}
	o := obs.Observation{At: parseTime(m["time"], now), Kind: obs.ISM, MAC: id, Name: model, Fields: map[string]string{}}
	if f, ok := num(m["freq"]); ok {
		o.MHz = int(math.Round(f))
	}
	if r, ok := num(m["rssi"]); ok {
		o.RSSI = int(math.Round(r)) // dB relative to full scale, not dBm
		if o.RSSI == 0 {
			o.RSSI = -1
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if !meta[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		o.Fields[k] = str(m[k])
	}
	if t := o.Fields["type"]; t != "" {
		o.Name = model + " " + t
	}
	return o, nil
}

// Read sends each rtl_433 line of r to fn. It skips lines it cannot read.
func Read(r io.Reader, fn func(obs.Observation) bool) (bad int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		o, err := Parse(sc.Bytes(), time.Now())
		if err != nil {
			bad++
			continue
		}
		if !fn(o) {
			return bad, nil
		}
	}
	return bad, sc.Err()
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// parseTime reads the time forms that rtl_433 -M time can write.
func parseTime(v any, now time.Time) time.Time {
	s := str(v)
	if s == "" {
		return now
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 1e9 {
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(frac*1e9))
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return now
}

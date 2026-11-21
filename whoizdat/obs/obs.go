// Package obs is the contract between sensors and the engine.
//
// A sensor writes one JSON Observation per line (JSONL). The engine reads
// lines. A sensor can be a goroutine, a separate process on this computer,
// or a process on another computer behind ssh. Nothing else couples them.
package obs

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Kind is the radio that heard the transmitter.
type Kind string

const (
	WiFi Kind = "wifi"
	BLE  Kind = "ble"
	ISM  Kind = "ism" // sub-GHz sensors, for example from rtl_433
)

// Frame says what the transmitter sent. Empty means unknown.
const (
	FrameBeacon    = "beacon"
	FrameProbeReq  = "probe-req"
	FrameProbeResp = "probe-resp"
	FrameData      = "data"
	FrameAdv       = "adv"
)

// Mfg is one BLE manufacturer-specific data record.
type Mfg struct {
	CompanyID int    `json:"company"`
	DataHex   string `json:"data,omitempty"`
}

// ServiceData is one BLE service data record.
type ServiceData struct {
	UUID    string `json:"uuid"`
	DataHex string `json:"data,omitempty"`
}

// Observation is one reception of one transmitter.
type Observation struct {
	At     time.Time `json:"at"`
	Sensor string    `json:"sensor,omitempty"`
	Kind   Kind      `json:"kind"`
	MAC    string    `json:"mac"` // a MAC address, or another stable id (see IsMAC)
	Frame  string    `json:"frame,omitempty"`
	Name   string    `json:"name,omitempty"` // SSID, probed SSID, or BLE local name
	Hidden bool      `json:"hidden,omitempty"`
	BSSID  string    `json:"bssid,omitempty"` // for a client: the access point it talks to
	RSSI   int       `json:"rssi,omitempty"`  // dBm, 0 = unknown
	Chan   int       `json:"chan,omitempty"`
	MHz    int       `json:"mhz,omitempty"`

	VendorIEs   []string      `json:"vendor_ies,omitempty"` // Wi-Fi vendor IE OUIs, "AABBCC"
	UUIDs       []string      `json:"uuids,omitempty"`
	Mfg         []Mfg         `json:"mfg,omitempty"`
	ServiceData []ServiceData `json:"svc_data,omitempty"`

	Fields map[string]string `json:"fields,omitempty"` // decoded values, for example tire pressure
}

// IsMAC is true when id is a 48-bit address. Other ids are, for example,
// "Schrader/1A2B3C4D" from rtl_433 or "uuid:..." from macOS Bluetooth.
// Address rules (OUI, MAC prefix) apply only to real addresses.
func IsMAC(id string) bool {
	if len(HexOnly(id)) != 12 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' || r == ':' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// NormMAC returns "AA:BB:CC:DD:EE:FF" for any common MAC spelling.
func NormMAC(raw string) string {
	hex := HexOnly(raw)
	if len(hex) < 2 {
		return strings.ToUpper(raw)
	}
	var b strings.Builder
	for i := 0; i < len(hex); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		end := min(i+2, len(hex))
		b.WriteString(hex[i:end])
	}
	return b.String()
}

// HexOnly keeps letters and digits and makes them upper case.
func HexOnly(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		}
	}
	return b.String()
}

// Randomized is true when the local bit is set and the group bit is clear.
func Randomized(mac string) bool {
	h := HexOnly(mac)
	if !IsMAC(mac) {
		return false
	}
	b := hexNibble(h[0])<<4 | hexNibble(h[1])
	return b&0x02 != 0 && b&0x01 == 0
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// Writer writes observations as JSONL. It is not safe for concurrent use.
type Writer struct{ enc *json.Encoder }

func NewWriter(w io.Writer) *Writer { return &Writer{enc: json.NewEncoder(w)} }

func (w *Writer) Write(o Observation) error { return w.enc.Encode(o) }

// Read sends each valid line of r to fn. It skips bad lines and counts them.
// It stops at EOF or when fn returns false.
func Read(r io.Reader, fn func(Observation) bool) (bad int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var o Observation
		if json.Unmarshal(line, &o) != nil || o.MAC == "" || o.Kind == "" {
			bad++
			continue
		}
		if IsMAC(o.MAC) {
			o.MAC = NormMAC(o.MAC)
		}
		if o.At.IsZero() {
			o.At = time.Now()
		}
		if !fn(o) {
			return bad, nil
		}
	}
	return bad, sc.Err()
}

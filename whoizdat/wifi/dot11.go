// Package wifi turns raw 802.11 frames into observations.
//
// It reads monitor-mode frames from a live Linux interface (AF_PACKET, no
// libpcap) or from a pcap or pcapng stream. The stream can come from a file,
// from stdin, or from another program, for example tcpdump on macOS.
package wifi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"whoizdat/obs"
)

// FrameDeauth is a deauthentication or disassociation frame.
const FrameDeauth = "deauth"

var errShort = errors.New("frame too short")

// Radiotap is the part of the radiotap header that this program uses.
type Radiotap struct {
	MHz    int
	RSSI   int // dBm, 0 = not present
	HasFCS bool
	BadFCS bool
	HdrLen int
}

// ParseRadiotap reads the radiotap header at the start of b.
func ParseRadiotap(b []byte) (Radiotap, error) {
	var rt Radiotap
	if len(b) < 8 || b[0] != 0 {
		return rt, errShort
	}
	rt.HdrLen = int(binary.LittleEndian.Uint16(b[2:4]))
	if rt.HdrLen < 8 || rt.HdrLen > len(b) {
		return rt, fmt.Errorf("bad radiotap length %d", rt.HdrLen)
	}
	present := binary.LittleEndian.Uint32(b[4:8])
	off := 8
	for p := present; p&(1<<31) != 0; { // more present words follow
		if off+4 > rt.HdrLen {
			return rt, errShort
		}
		p = binary.LittleEndian.Uint32(b[off : off+4])
		off += 4
	}
	h := b[:rt.HdrLen]
	align := func(n int) {
		if r := off % n; r != 0 {
			off += n - r
		}
	}
	// Fields 0 to 5 are in this order with these sizes and alignments.
	if present&(1<<0) != 0 { // TSFT
		align(8)
		off += 8
	}
	if present&(1<<1) != 0 { // Flags
		if off >= len(h) {
			return rt, errShort
		}
		rt.HasFCS = h[off]&0x10 != 0
		rt.BadFCS = h[off]&0x40 != 0
		off++
	}
	if present&(1<<2) != 0 { // Rate
		off++
	}
	if present&(1<<3) != 0 { // Channel: frequency, flags
		align(2)
		if off+4 > len(h) {
			return rt, errShort
		}
		rt.MHz = int(binary.LittleEndian.Uint16(h[off : off+2]))
		off += 4
	}
	if present&(1<<4) != 0 { // FHSS
		off += 2
	}
	if present&(1<<5) != 0 { // antenna signal, dBm
		if off >= len(h) {
			return rt, errShort
		}
		rt.RSSI = int(int8(h[off]))
	}
	return rt, nil
}

// Channel returns the channel number for a frequency in MHz, or 0.
func Channel(mhz int) int {
	switch {
	case mhz == 2484:
		return 14
	case mhz >= 2412 && mhz <= 2472:
		return (mhz - 2407) / 5
	case mhz >= 5160 && mhz <= 5885:
		return (mhz - 5000) / 5
	case mhz >= 5955 && mhz <= 7115:
		return (mhz - 5950) / 5
	}
	return 0
}

// FromRadiotap parses a radiotap frame. ok is false when the frame has no
// transmitter this program reports (control frames, broadcast senders, bad FCS).
func FromRadiotap(b []byte, at time.Time) (o obs.Observation, ok bool) {
	rt, err := ParseRadiotap(b)
	if err != nil || rt.BadFCS {
		return o, false
	}
	f := b[rt.HdrLen:]
	if rt.HasFCS && len(f) >= 4 {
		f = f[:len(f)-4]
	}
	o, ok = FromDot11(f, at)
	if ok {
		o.RSSI = rt.RSSI
		o.MHz = rt.MHz
		if c := Channel(rt.MHz); c != 0 && o.Chan == 0 {
			o.Chan = c
		}
	}
	return o, ok
}

// FromDot11 parses an 802.11 frame with no radiotap header.
func FromDot11(f []byte, at time.Time) (o obs.Observation, ok bool) {
	if len(f) < 24 {
		return o, false
	}
	typ := (f[0] >> 2) & 0x3
	sub := (f[0] >> 4) & 0xf
	toDS, fromDS := f[1]&0x01 != 0, f[1]&0x02 != 0
	addr2 := f[10:16]
	if addr2[0]&0x01 != 0 { // a group address cannot send
		return o, false
	}
	o = obs.Observation{At: at, Kind: obs.WiFi, MAC: mac(addr2)}
	switch typ {
	case 0: // management
		var ies []byte
		switch sub {
		case 8, 5: // beacon, probe response
			if len(f) < 36 {
				return o, false
			}
			o.Frame = obs.FrameBeacon
			if sub == 5 {
				o.Frame = obs.FrameProbeResp
			}
			ies = f[36:]
		case 4: // probe request
			o.Frame = obs.FrameProbeReq
			ies = f[24:]
		case 10, 12: // disassociation, deauthentication
			o.Frame = FrameDeauth
			o.BSSID = mac(f[16:22])
			return o, true
		default:
			return o, false
		}
		parseIEs(&o, ies)
		return o, true
	case 2: // data: report only a client that sends to the access point
		if !toDS || fromDS {
			return o, false
		}
		o.Frame = obs.FrameData
		o.BSSID = mac(f[4:10])
		return o, true
	}
	return o, false
}

func parseIEs(o *obs.Observation, b []byte) {
	sawSSID := false
	for len(b) >= 2 {
		id, n := b[0], int(b[1])
		if 2+n > len(b) {
			break
		}
		v := b[2 : 2+n]
		switch id {
		case 0:
			if !sawSSID {
				sawSSID = true
				o.Name, o.Hidden = ssid(v)
				if o.Frame == obs.FrameProbeReq {
					o.Hidden = false // an empty probe is a wildcard, not a hidden network
				}
			}
		case 3:
			if n == 1 {
				o.Chan = int(v[0])
			}
		case 221:
			if n >= 3 {
				oui := fmt.Sprintf("%02X%02X%02X", v[0], v[1], v[2])
				if !contains(o.VendorIEs, oui) {
					o.VendorIEs = append(o.VendorIEs, oui)
				}
			}
		}
		b = b[2+n:]
	}
}

// ssid returns the printable name and true when the network hides its name.
func ssid(v []byte) (string, bool) {
	zero := true
	for _, c := range v {
		if c != 0 {
			zero = false
			break
		}
	}
	if zero {
		return "", true
	}
	s := strings.ToValidUTF8(string(v), "?")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s), false
}

func mac(b []byte) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[0], b[1], b[2], b[3], b[4], b[5])
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

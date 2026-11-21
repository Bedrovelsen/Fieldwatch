package wifi

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
	"time"

	"whoizdat/obs"
)

// radiotap: present = TSFT, Flags, Channel, dBm signal.
func radiotap(mhz int, dbm int8, fcs bool) []byte {
	b := make([]byte, 8, 32)
	b[0] = 0
	binary.LittleEndian.PutUint32(b[4:8], 1<<0|1<<1|1<<3|1<<5)
	b = append(b, make([]byte, 8)...) // TSFT at 8 (aligned)
	flags := byte(0)
	if fcs {
		flags = 0x10
	}
	b = append(b, flags) // offset 16
	b = append(b, 0)     // pad to 2 for channel
	b = binary.LittleEndian.AppendUint16(b, uint16(mhz))
	b = binary.LittleEndian.AppendUint16(b, 0x0080)
	b = append(b, byte(dbm))
	binary.LittleEndian.PutUint16(b[2:4], uint16(len(b)))
	return b
}

var (
	ap     = []byte{0x60, 0x60, 0x1F, 0x11, 0x22, 0x33}
	client = []byte{0xDA, 0xA1, 0x19, 0x01, 0x02, 0x03}
	bcast  = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)

func ie(id byte, v ...byte) []byte { return append([]byte{id, byte(len(v))}, v...) }

func mgmt(sub byte, a1, a2, a3 []byte, body ...[]byte) []byte {
	f := []byte{sub << 4, 0, 0, 0}
	f = append(f, a1...)
	f = append(f, a2...)
	f = append(f, a3...)
	f = append(f, 0, 0) // sequence
	for _, b := range body {
		f = append(f, b...)
	}
	return f
}

func beacon(name string) []byte {
	fixed := make([]byte, 12)
	return mgmt(8, bcast, ap, ap, fixed,
		ie(0, []byte(name)...),
		ie(3, 6),
		ie(221, 0x00, 0x50, 0xF2, 0x01),
		ie(221, 0x26, 0x37, 0x12, 0x99))
}

func TestBeaconWithRadiotap(t *testing.T) {
	frame := append(radiotap(2437, -42, true), beacon("home")...)
	frame = append(frame, 1, 2, 3, 4) // FCS
	o, ok := FromRadiotap(frame, time.Unix(1, 0))
	if !ok {
		t.Fatal("beacon not decoded")
	}
	if o.MAC != "60:60:1F:11:22:33" || o.Frame != obs.FrameBeacon || o.Name != "home" ||
		o.RSSI != -42 || o.MHz != 2437 || o.Chan != 6 || o.Hidden {
		t.Errorf("got %+v", o)
	}
	if !slices.Equal(o.VendorIEs, []string{"0050F2", "263712"}) {
		t.Errorf("vendor IEs %v", o.VendorIEs)
	}
}

func TestHiddenAndProbe(t *testing.T) {
	o, ok := FromDot11(mgmt(8, bcast, ap, ap, make([]byte, 12), ie(0, 0, 0, 0)), time.Now())
	if !ok || !o.Hidden || o.Name != "" {
		t.Errorf("hidden beacon: %v %+v", ok, o)
	}
	o, ok = FromDot11(mgmt(4, bcast, client, bcast, ie(0, []byte("CoffeeShop")...)), time.Now())
	if !ok || o.Frame != obs.FrameProbeReq || o.Name != "CoffeeShop" || o.MAC != "DA:A1:19:01:02:03" {
		t.Errorf("probe: %v %+v", ok, o)
	}
	o, ok = FromDot11(mgmt(4, bcast, client, bcast, ie(0)), time.Now())
	if !ok || o.Hidden || o.Name != "" {
		t.Errorf("wildcard probe: %v %+v", ok, o)
	}
}

func TestDataAndDeauth(t *testing.T) {
	data := mgmt(0, ap, client, ap)
	data[0] = 2<<2 | 0<<4 // data
	data[1] = 0x01        // to DS
	o, ok := FromDot11(data, time.Now())
	if !ok || o.Frame != obs.FrameData || o.MAC != "DA:A1:19:01:02:03" || o.BSSID != "60:60:1F:11:22:33" {
		t.Errorf("data: %v %+v", ok, o)
	}
	data[1] = 0x02 // from DS: the access point sends, skip it
	if _, ok := FromDot11(data, time.Now()); ok {
		t.Error("from-DS data must be skipped")
	}
	o, ok = FromDot11(mgmt(12, client, ap, ap, []byte{7, 0}), time.Now())
	if !ok || o.Frame != FrameDeauth || o.BSSID != "60:60:1F:11:22:33" {
		t.Errorf("deauth: %v %+v", ok, o)
	}
	if _, ok := FromDot11(mgmt(8, ap, bcast, ap, make([]byte, 12)), time.Now()); ok {
		t.Error("a group address cannot send")
	}
}

func TestChannel(t *testing.T) {
	for mhz, c := range map[int]int{2412: 1, 2472: 13, 2484: 14, 5180: 36, 5825: 165, 5955: 1, 6115: 33, 900: 0} {
		if Channel(mhz) != c {
			t.Errorf("Channel(%d) = %d, want %d", mhz, Channel(mhz), c)
		}
	}
}

func TestPcap(t *testing.T) {
	var b bytes.Buffer
	le := binary.LittleEndian
	hdr := make([]byte, 24)
	le.PutUint32(hdr[0:], 0xa1b2c3d4)
	le.PutUint16(hdr[4:], 2)
	le.PutUint16(hdr[6:], 4)
	le.PutUint32(hdr[16:], 65535)
	le.PutUint32(hdr[20:], LinkRadiotap)
	b.Write(hdr)
	frame := append(radiotap(5180, -70, false), beacon("five")...)
	rec := make([]byte, 16)
	le.PutUint32(rec[0:], 1700000000)
	le.PutUint32(rec[4:], 500000)
	le.PutUint32(rec[8:], uint32(len(frame)))
	le.PutUint32(rec[12:], uint32(len(frame)))
	b.Write(rec)
	b.Write(frame)

	var got []obs.Observation
	err := ReadCapture(&b, func(p Packet) bool {
		if o, ok := Decode(p); ok {
			got = append(got, o)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "five" || got[0].Chan != 6 || got[0].MHz != 5180 ||
		!got[0].At.Equal(time.Unix(1700000000, 500000000)) {
		t.Errorf("got %+v", got)
	}
}

func TestPcapng(t *testing.T) {
	le := binary.LittleEndian
	block := func(typ uint32, body []byte) []byte {
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
		n := uint32(12 + len(body))
		out := le.AppendUint32(nil, typ)
		out = le.AppendUint32(out, n)
		out = append(out, body...)
		return le.AppendUint32(out, n)
	}
	var b bytes.Buffer
	shb := le.AppendUint32(nil, 0x1A2B3C4D)
	shb = append(shb, 1, 0, 0, 0)
	shb = le.AppendUint64(shb, ^uint64(0))
	b.Write(block(0x0A0D0D0A, shb))
	idb := le.AppendUint16(nil, LinkDot11)
	idb = append(idb, 0, 0)
	idb = le.AppendUint32(idb, 65535)
	idb = append(idb, 9, 0, 1, 0, 9, 0, 0, 0) // if_tsresol = 10^-9
	idb = append(idb, 0, 0, 0, 0)             // end of options
	b.Write(block(1, idb))
	frame := beacon("ng")
	ts := uint64(1700000000) * 1e9
	epb := le.AppendUint32(nil, 0)
	epb = le.AppendUint32(epb, uint32(ts>>32))
	epb = le.AppendUint32(epb, uint32(ts))
	epb = le.AppendUint32(epb, uint32(len(frame)))
	epb = le.AppendUint32(epb, uint32(len(frame)))
	epb = append(epb, frame...)
	b.Write(block(6, epb))

	var got []obs.Observation
	if err := ReadCapture(&b, func(p Packet) bool {
		if o, ok := Decode(p); ok {
			got = append(got, o)
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "ng" || !got[0].At.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("got %+v", got)
	}
}

func TestParseChannels(t *testing.T) {
	got, err := ParseChannels("1,6, 11,36-48")
	if err != nil || !slices.Equal(got, []int{1, 6, 11, 36, 40, 44, 48}) {
		t.Errorf("got %v %v", got, err)
	}
	if _, err := ParseChannels("x"); err == nil {
		t.Error("want error")
	}
}

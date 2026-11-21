package wifi

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"whoizdat/obs"
)

// Link types this program reads.
const (
	LinkDot11    = 105
	LinkRadiotap = 127
)

// Packet is one captured frame.
type Packet struct {
	At   time.Time
	Link int
	Data []byte
}

const maxPacket = 256 * 1024

// ReadCapture reads a classic pcap or a pcapng stream and calls fn for each
// packet. It stops at EOF, at an error, or when fn returns false.
func ReadCapture(r io.Reader, fn func(Packet) bool) error {
	br := bufio.NewReaderSize(r, 1<<16)
	magic, err := br.Peek(4)
	if err != nil {
		return err
	}
	switch binary.LittleEndian.Uint32(magic) {
	case 0x0A0D0D0A:
		return readPcapng(br, fn)
	case 0xa1b2c3d4, 0xd4c3b2a1, 0xa1b23c4d, 0x4d3cb2a1:
		return readPcap(br, fn)
	}
	return fmt.Errorf("not a pcap or pcapng stream (magic % x)", magic)
}

func readPcap(r io.Reader, fn func(Packet) bool) error {
	var gh [24]byte
	if _, err := io.ReadFull(r, gh[:]); err != nil {
		return err
	}
	var bo binary.ByteOrder = binary.LittleEndian
	m := bo.Uint32(gh[0:4])
	if m == 0xd4c3b2a1 || m == 0x4d3cb2a1 {
		bo = binary.BigEndian
		m = bo.Uint32(gh[0:4])
	}
	nano := m == 0xa1b23c4d
	link := int(bo.Uint32(gh[20:24]) & 0xffff)
	var rh [16]byte
	for {
		if _, err := io.ReadFull(r, rh[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		sec, frac := int64(bo.Uint32(rh[0:4])), int64(bo.Uint32(rh[4:8]))
		n := bo.Uint32(rh[8:12])
		if n > maxPacket {
			return fmt.Errorf("packet length %d is too large", n)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		if !nano {
			frac *= 1000
		}
		if !fn(Packet{At: time.Unix(sec, frac), Link: link, Data: data}) {
			return nil
		}
	}
}

func readPcapng(r io.Reader, fn func(Packet) bool) error {
	var bo binary.ByteOrder = binary.LittleEndian
	type iface struct {
		link int
		res  time.Duration // one timestamp unit
	}
	var ifaces []iface
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		typ := binary.LittleEndian.Uint32(hdr[0:4])
		if typ == 0x0A0D0D0A { // section header: find the byte order
			var bom [4]byte
			if _, err := io.ReadFull(r, bom[:]); err != nil {
				return err
			}
			if binary.BigEndian.Uint32(bom[:]) == 0x1A2B3C4D {
				bo = binary.BigEndian
			} else {
				bo = binary.LittleEndian
			}
			n := bo.Uint32(hdr[4:8])
			if n < 16 || n > maxPacket {
				return fmt.Errorf("bad pcapng section length %d", n)
			}
			if _, err := io.CopyN(io.Discard, r, int64(n-12)); err != nil {
				return err
			}
			ifaces = nil
			continue
		}
		typ = bo.Uint32(hdr[0:4])
		n := bo.Uint32(hdr[4:8])
		if n < 12 || n > maxPacket || n%4 != 0 {
			return fmt.Errorf("bad pcapng block length %d", n)
		}
		body := make([]byte, n-8)
		if _, err := io.ReadFull(r, body); err != nil {
			return err
		}
		body = body[:len(body)-4] // trailing length
		switch typ {
		case 1: // interface description
			if len(body) < 8 {
				return errShort
			}
			ifc := iface{link: int(bo.Uint16(body[0:2])), res: time.Microsecond}
			for opt := body[8:]; len(opt) >= 4; {
				code, l := bo.Uint16(opt[0:2]), int(bo.Uint16(opt[2:4]))
				if 4+l > len(opt) || code == 0 {
					break
				}
				if code == 9 && l >= 1 { // if_tsresol
					v := opt[4]
					if v&0x80 == 0 {
						d := time.Second
						for range v {
							d /= 10
						}
						ifc.res = max(d, time.Nanosecond)
					}
				}
				opt = opt[4+((l+3)&^3):]
			}
			ifaces = append(ifaces, ifc)
		case 6: // enhanced packet
			if len(body) < 20 {
				return errShort
			}
			id := int(bo.Uint32(body[0:4]))
			ts := uint64(bo.Uint32(body[4:8]))<<32 | uint64(bo.Uint32(body[8:12]))
			capLen := int(bo.Uint32(body[12:16]))
			if id >= len(ifaces) || 20+capLen > len(body) {
				return fmt.Errorf("bad pcapng packet block")
			}
			ifc := ifaces[id]
			at := time.Unix(0, 0).Add(time.Duration(ts) * ifc.res)
			if !fn(Packet{At: at, Link: ifc.link, Data: body[20 : 20+capLen]}) {
				return nil
			}
		}
	}
}

// Decode turns a packet into an observation.
func Decode(p Packet) (obs.Observation, bool) {
	switch p.Link {
	case LinkRadiotap:
		return FromRadiotap(p.Data, p.At)
	case LinkDot11:
		return FromDot11(p.Data, p.At)
	}
	return obs.Observation{}, false
}

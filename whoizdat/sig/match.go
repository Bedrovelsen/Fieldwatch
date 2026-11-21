package sig

import (
	"strings"

	"whoizdat/obs"
)

// Radio is what the matcher needs to know about one transmitter.
// The engine fills it from all observations of that transmitter.
type Radio struct {
	Kind        obs.Kind
	MAC         string
	Name        string
	Hidden      bool
	UUIDs       []string
	Mfg         []obs.Mfg
	ServiceData []obs.ServiceData
	VendorIEs   []string
}

// WPA (Microsoft) and RSN (IEEE) vendor IEs are protocol tags, not products.
var protocolIE = map[string]bool{"0050F2": true, "000FAC": true}

const btBaseTail = "00001000800000805F9B34FB"

type prefixRef struct {
	fleet  int
	prefix string // hex only, length >= 6
	rule   Rule
}

// Matcher is a compiled catalog. It is safe for concurrent use after New.
type Matcher struct {
	cat    *Catalog
	byHex6 map[string][]prefixRef // OR signatures: address and vendor IE prefixes
	other  [][]Rule               // OR signatures: rules not in byHex6, by fleet index
	and    []int                  // AND signatures
}

func New(c *Catalog) *Matcher {
	m := &Matcher{cat: c, byHex6: map[string][]prefixRef{}, other: make([][]Rule, len(c.Fleets))}
	for i, f := range c.Fleets {
		if !f.on() || f.Cluster() {
			continue
		}
		if !f.any() {
			if !hasForeign(f.Rules) {
				m.and = append(m.and, i)
			}
			continue
		}
		for _, r := range f.Rules {
			if !r.on() || !known(r) {
				continue
			}
			p := obs.HexOnly(r.Text)
			if (r.Kind == OUI || r.Kind == MACPrefix || r.Kind == VendorIEOUI) && len(p) >= 6 {
				ref := prefixRef{fleet: i, prefix: p, rule: r}
				m.byHex6[p[:6]] = append(m.byHex6[p[:6]], ref)
				continue
			}
			m.other[i] = append(m.other[i], r)
		}
	}
	return m
}

func (m *Matcher) Catalog() *Catalog { return m.cat }

// Match returns the ids of the signatures that this radio matches, in
// catalog order.
func (m *Matcher) Match(r Radio) []string {
	hit := map[int]bool{}
	mac := obs.HexOnly(r.MAC)
	if len(mac) >= 6 {
		m.prefixHits(hit, r, mac, fromMAC)
	}
	if r.Kind == obs.WiFi {
		if u := universalOUI(mac); u != "" {
			m.prefixHits(hit, r, u, fromRecovered)
		}
		for _, ie := range r.VendorIEs {
			h := obs.HexOnly(ie)
			if len(h) >= 6 && !protocolIE[h[:6]] {
				m.prefixHits(hit, r, h, fromIE)
			}
		}
	}
	for i, rules := range m.other {
		if hit[i] {
			continue
		}
		for _, rule := range rules {
			if ruleHits(r, rule) {
				hit[i] = true
				break
			}
		}
	}
	for _, i := range m.and {
		ok, n := true, 0
		for _, rule := range m.cat.Fleets[i].Rules {
			if !rule.on() || !appliesTo(rule, r.Kind) {
				continue
			}
			n++
			if !ruleHits(r, rule) {
				ok = false
				break
			}
		}
		if ok && n > 0 {
			hit[i] = true
		}
	}
	var ids []string
	for i := range m.cat.Fleets {
		if hit[i] {
			ids = append(ids, m.cat.Fleets[i].ID)
		}
	}
	return ids
}

// Where the prefix came from.
const (
	fromMAC = iota
	fromIE
	fromRecovered // the address with the local bit cleared
)

// prefixHits marks OR signatures with an address or vendor IE rule that matches h.
func (m *Matcher) prefixHits(hit map[int]bool, r Radio, h string, from int) {
	for _, ref := range m.byHex6[h[:6]] {
		if hit[ref.fleet] || !strings.HasPrefix(h, ref.prefix) || !appliesTo(ref.rule, r.Kind) {
			continue
		}
		k := ref.rule.Kind
		ok := false
		switch from {
		case fromMAC:
			ok = k == OUI || k == MACPrefix
		case fromIE:
			ok = (k == OUI || k == VendorIEOUI) && r.Kind == obs.WiFi
		case fromRecovered:
			ok = k == OUI && len(ref.prefix) == 6 && r.Kind == obs.WiFi
		}
		if ok {
			hit[ref.fleet] = true
		}
	}
}

func known(r Rule) bool {
	switch r.Kind {
	case OUI, MACPrefix, NameContains, NameGlob, ServiceUUID, ServiceDataRule,
		ManufacturerID, ManufacturerDat, RadioKind, HiddenSSID, VendorIEOUI:
	default:
		return false
	}
	return r.Radio == nil || *r.Radio == "WIFI" || *r.Radio == "BLE"
}

func hasForeign(rules []Rule) bool {
	for _, r := range rules {
		if r.on() && !known(r) {
			return true
		}
	}
	return false
}

func kindName(k obs.Kind) string {
	if k == obs.BLE {
		return "BLE"
	}
	return "WIFI"
}

// appliesTo: a missing radio on a service or company rule means BLE only.
// A missing radio on a hidden network or vendor IE rule means Wi-Fi only.
// A missing radio on a name or address rule means both.
func appliesTo(r Rule, k obs.Kind) bool {
	if !known(r) {
		return false
	}
	if r.Kind == RadioKind {
		return r.Radio == nil || *r.Radio == kindName(k)
	}
	if r.Radio != nil {
		return *r.Radio == kindName(k)
	}
	switch r.Kind {
	case ServiceUUID, ServiceDataRule, ManufacturerID, ManufacturerDat:
		return k == obs.BLE
	case HiddenSSID, VendorIEOUI:
		return k == obs.WiFi
	}
	return true
}

func ruleHits(r Radio, rule Rule) bool {
	if !appliesTo(rule, r.Kind) {
		return false
	}
	switch rule.Kind {
	case OUI, MACPrefix:
		p := obs.HexOnly(rule.Text)
		if p == "" {
			return false
		}
		mac := obs.HexOnly(r.MAC)
		if strings.HasPrefix(mac, p) {
			return true
		}
		if rule.Kind == OUI && r.Kind == obs.WiFi {
			if len(p) == 6 && universalOUI(mac) == p {
				return true
			}
			return vendorIEHit(r, p)
		}
		return false
	case NameContains:
		return strings.TrimSpace(r.Name) != "" && strings.TrimSpace(rule.Text) != "" &&
			strings.Contains(strings.ToLower(r.Name), strings.ToLower(rule.Text))
	case NameGlob:
		return strings.TrimSpace(r.Name) != "" && Glob(rule.Text, r.Name)
	case ServiceUUID:
		want := CanonUUID(rule.Text)
		for _, u := range r.UUIDs {
			if CanonUUID(u) == want {
				return true
			}
		}
		for _, sd := range r.ServiceData {
			if CanonUUID(sd.UUID) == want {
				return true
			}
		}
	case ManufacturerID:
		for _, m := range r.Mfg {
			if m.CompanyID == rule.CompanyID {
				return true
			}
		}
	case ManufacturerDat:
		p := obs.HexOnly(rule.DataPrefixHex)
		for _, m := range r.Mfg {
			if p != "" && (rule.CompanyID == 0 || m.CompanyID == rule.CompanyID) &&
				strings.HasPrefix(obs.HexOnly(m.DataHex), p) {
				return true
			}
		}
	case ServiceDataRule:
		return serviceDataHit(r, rule)
	case RadioKind:
		return true // appliesTo already checked the radio
	case HiddenSSID:
		return r.Hidden
	case VendorIEOUI:
		return vendorIEHit(r, obs.HexOnly(rule.Text))
	}
	return false
}

// serviceDataHit: with a UUID, the data must start with the prefix. With no
// UUID, the data must contain the prefix in either byte order.
func serviceDataHit(r Radio, rule Rule) bool {
	p := obs.HexOnly(rule.DataPrefixHex)
	want := ""
	if strings.TrimSpace(rule.Text) != "" {
		want = CanonUUID(rule.Text)
	}
	if p == "" && want == "" {
		return false
	}
	contains := want == "" && p != ""
	needles := []string{p}
	if contains {
		if rev := reverseBytes(p); rev != "" && rev != p {
			needles = append(needles, rev)
		}
	}
	for _, sd := range r.ServiceData {
		if want != "" && CanonUUID(sd.UUID) != want {
			continue
		}
		d := obs.HexOnly(sd.DataHex)
		for _, n := range needles {
			if (contains && strings.Contains(d, n)) || (!contains && strings.HasPrefix(d, n)) {
				return true
			}
		}
	}
	return false
}

func vendorIEHit(r Radio, want string) bool {
	if r.Kind != obs.WiFi || len(want) < 6 || protocolIE[want[:6]] {
		return false
	}
	for _, ie := range r.VendorIEs {
		h := obs.HexOnly(ie)
		if len(h) >= 6 && !protocolIE[h[:6]] && strings.HasPrefix(h, want) {
			return true
		}
	}
	return false
}

// universalOUI returns the 24-bit OUI with the local bit cleared, for virtual
// BSSIDs (guest, mesh). It is "" for a universal or group address.
func universalOUI(macHex string) string {
	if len(macHex) < 6 {
		return ""
	}
	b := hexByte(macHex[0], macHex[1])
	if b&0x02 == 0 || b&0x01 != 0 {
		return ""
	}
	const digits = "0123456789ABCDEF"
	b &^= 0x02
	return string([]byte{digits[b>>4], digits[b&0x0f]}) + macHex[2:6]
}

func hexByte(a, b byte) byte {
	n := func(c byte) byte {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10
		}
		return 0
	}
	return n(a)<<4 | n(b)
}

func reverseBytes(h string) string {
	if len(h) < 2 || len(h)%2 != 0 {
		return ""
	}
	out := make([]byte, 0, len(h))
	for i := len(h); i >= 2; i -= 2 {
		out = append(out, h[i-2:i]...)
	}
	return string(out)
}

// CanonUUID makes 16-bit, 32-bit and 128-bit UUID spellings comparable.
func CanonUUID(raw string) string {
	h := obs.HexOnly(raw)
	if strings.HasPrefix(h, "0X") {
		h = h[2:]
	}
	switch len(h) {
	case 4:
		return "0000" + h + btBaseTail
	case 8:
		return h + btBaseTail
	}
	return h
}

// Glob matches * and ? without regard to case.
func Glob(pattern, text string) bool {
	if strings.TrimSpace(pattern) == "" {
		return false
	}
	p, t := []rune(strings.ToLower(pattern)), []rune(strings.ToLower(text))
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

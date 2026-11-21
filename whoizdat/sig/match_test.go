package sig

import (
	"slices"
	"testing"

	"whoizdat/obs"
)

const stock = "../../dist/fieldwatch-signatures.rsi"

func stockMatcher(t *testing.T) *Matcher {
	t.Helper()
	c, err := Load(stock)
	if err != nil {
		t.Fatalf("load stock catalog: %v", err)
	}
	if len(c.Fleets) < 250 {
		t.Fatalf("stock catalog has %d signatures, want more than 250", len(c.Fleets))
	}
	return New(c)
}

func TestStockCatalog(t *testing.T) {
	m := stockMatcher(t)
	cases := []struct {
		name string
		r    Radio
		want string
		not  bool
	}{
		{"name glob, any radio", Radio{Kind: obs.BLE, MAC: "11:22:33:44:55:66", Name: "70mai_A800"}, "fleet-70mai", false},
		{"name glob is case blind", Radio{Kind: obs.WiFi, MAC: "11:22:33:44:55:66", Name: "70MAI-dash"}, "fleet-70mai", false},
		{"Wi-Fi OUI", Radio{Kind: obs.WiFi, MAC: "00:0F:B3:01:02:03"}, "fleet-actiontec", false},
		{"Wi-Fi OUI rule does not apply to BLE", Radio{Kind: obs.BLE, MAC: "00:0F:B3:01:02:03"}, "fleet-actiontec", true},
		{"virtual BSSID with local bit", Radio{Kind: obs.WiFi, MAC: "02:0F:B3:01:02:03"}, "fleet-actiontec", false},
		{"BLE company id", Radio{Kind: obs.BLE, MAC: "C0:00:00:00:00:01", Mfg: []obs.Mfg{{CompanyID: 838}}}, "fleet-acuity", false},
		{"BLE 128-bit service", Radio{Kind: obs.BLE, MAC: "C0:00:00:00:00:01", UUIDs: []string{"7fc4c6d5-8fab-452a-9871-8d707c939a6f"}}, "fleet-acuity", false},
		{"service data contains", Radio{Kind: obs.BLE, MAC: "C0:00:00:00:00:02", ServiceData: []obs.ServiceData{{UUID: "FE6C", DataHex: "0102425743444556494345FF"}}}, "fleet-axon", false},
		{"vendor IE", Radio{Kind: obs.WiFi, MAC: "60:60:1F:00:00:01", VendorIEs: []string{"263712"}}, "fleet-dji", false},
		{"full MAC prefix", Radio{Kind: obs.WiFi, MAC: "de:ad:be:ef:de:ad"}, "fleet-pwnagotchi", false},
		{"name contains, Wi-Fi only", Radio{Kind: obs.WiFi, MAC: "11:22:33:44:55:66", Name: "my-airlink-router"}, "fleet-airlink", false},
		{"name contains, not on BLE", Radio{Kind: obs.BLE, MAC: "11:22:33:44:55:66", Name: "my-airlink-router"}, "fleet-airlink", true},
	}
	for _, tc := range cases {
		got := m.Match(tc.r)
		if has := slices.Contains(got, tc.want); has == tc.not {
			t.Errorf("%s: Match = %v, want %s present=%v", tc.name, got, tc.want, !tc.not)
		}
	}
}

func TestANDSignature(t *testing.T) {
	yes, ble := true, "BLE"
	c := &Catalog{Format: "radio-signatures", Fleets: []Fleet{{
		ID: "both", MatchAny: new(bool),
		Rules: []Rule{
			{Kind: NameGlob, Text: "cam*"},
			{Kind: ManufacturerID, CompanyID: 76, Radio: &ble, Enabled: &yes},
		},
	}}}
	m := New(c)
	if got := m.Match(Radio{Kind: obs.BLE, Name: "cam1", MAC: "AA", Mfg: []obs.Mfg{{CompanyID: 76}}}); len(got) != 1 {
		t.Errorf("both rules hit: got %v", got)
	}
	if got := m.Match(Radio{Kind: obs.BLE, Name: "cam1", MAC: "AA"}); len(got) != 0 {
		t.Errorf("one rule hit: got %v", got)
	}
	// On Wi-Fi only the name rule applies.
	if got := m.Match(Radio{Kind: obs.WiFi, Name: "cam1", MAC: "AA"}); len(got) != 1 {
		t.Errorf("Wi-Fi with name: got %v", got)
	}
}

func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		p, s string
		ok   bool
	}{
		{"a*", "abc", true}, {"*c", "abc", true}, {"a?c", "abc", true}, {"a?c", "ac", false},
		{"*b*", "abc", true}, {"abc", "abcd", false}, {"", "x", false}, {"a.c", "abc", false},
		{"*", "", true}, {"DJI-*", "dji-mavic", true},
	} {
		if Glob(tc.p, tc.s) != tc.ok {
			t.Errorf("Glob(%q, %q) = %v", tc.p, tc.s, !tc.ok)
		}
	}
}

func TestCanonUUID(t *testing.T) {
	a := CanonUUID("FEAA")
	for _, s := range []string{"0xfeaa", "0000FEAA", "0000feaa-0000-1000-8000-00805f9b34fb"} {
		if CanonUUID(s) != a {
			t.Errorf("CanonUUID(%q) = %q, want %q", s, CanonUUID(s), a)
		}
	}
}

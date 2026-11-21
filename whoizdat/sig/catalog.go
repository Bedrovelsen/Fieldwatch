// Package sig loads radio-signatures (RSI-1) catalogs and matches radios
// against them. The rules follow the Fieldwatch SignatureEngine:
// app/src/main/java/app/fieldwatch/domain/SignatureEngine.kt.
package sig

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Rule kinds in the file.
const (
	OUI             = "OUI"
	MACPrefix       = "MAC_PREFIX"
	NameContains    = "NAME_CONTAINS"
	NameGlob        = "NAME_GLOB"
	ServiceUUID     = "SERVICE_UUID"
	ServiceDataRule = "SERVICE_DATA"
	ManufacturerID  = "MANUFACTURER_ID"
	ManufacturerDat = "MANUFACTURER_DATA"
	RadioKind       = "RADIO_KIND"
	HiddenSSID      = "HIDDEN_SSID"
	VendorIEOUI     = "VENDOR_IE_OUI"
)

type Rule struct {
	Kind          string  `json:"kind"`
	Text          string  `json:"text"`
	CompanyID     int     `json:"companyId"`
	DataPrefixHex string  `json:"dataPrefixHex"`
	Radio         *string `json:"radio"` // "WIFI", "BLE", or null
	Enabled       *bool   `json:"enabled"`
}

func (r Rule) on() bool { return r.Enabled == nil || *r.Enabled }

// Fleet is one signature. The file calls it a fleet.
type Fleet struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Enabled       *bool  `json:"enabled"`
	MatchAny      *bool  `json:"matchAny"`
	Rules         []Rule `json:"rules"`
	MinPeers      int    `json:"minPeers"`
	ClusterByOui  bool   `json:"clusterByOui"`
	SequentialMac bool   `json:"sequentialMac"`
	Notes         string `json:"notes"`
	AttentionNote string `json:"attentionNote"`
	Class         string `json:"kind"`
	Publisher     string `json:"publisher"`
}

func (f Fleet) on() bool  { return f.Enabled == nil || *f.Enabled }
func (f Fleet) any() bool { return f.MatchAny == nil || *f.MatchAny }

// Cluster signatures need many radios at once. This version does not apply them.
func (f Fleet) Cluster() bool { return f.MinPeers > 0 || f.ClusterByOui || f.SequentialMac }

type Catalog struct {
	Format         string  `json:"format"`
	FormatVersion  int     `json:"formatVersion"`
	CatalogVersion int     `json:"catalogVersion"`
	Publisher      string  `json:"publisher"`
	Fleets         []Fleet `json:"fleets"`
}

// Load reads a catalog file. The file can be gzip compressed.
func Load(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Catalog, error) {
	if len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if b, err = io.ReadAll(io.LimitReader(zr, 64<<20)); err != nil {
			return nil, err
		}
	}
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Format != "radio-signatures" {
		return nil, fmt.Errorf("not a radio-signatures file (format %q)", c.Format)
	}
	if c.FormatVersion > 1 {
		return nil, fmt.Errorf("radio-signatures format %d is newer than this program", c.FormatVersion)
	}
	return &c, nil
}

// ByID returns the signature with this id, or nil.
func (c *Catalog) ByID(id string) *Fleet {
	for i := range c.Fleets {
		if c.Fleets[i].ID == id {
			return &c.Fleets[i]
		}
	}
	return nil
}

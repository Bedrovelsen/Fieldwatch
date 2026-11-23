// Package geo places radios on a map around a fixed sensor.
//
// One sensor gives a signal level, not a direction. Distance comes from the
// log-distance path-loss model: d = 10^((ref - rssi) / (10 n)), where ref is
// the level at 1 m and n is the path-loss exponent (2 in open air, 3 to 4
// through walls). The result is a band from a near to a far distance, not a
// point. The direction on the map is made up from a hash of the radio id, so a
// radio keeps the same place between updates.
package geo

import (
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
)

// Point is a position in degrees.
type Point struct{ Lat, Lon float64 }

// ParsePoint reads "lat,lon".
func ParsePoint(s string) (Point, error) {
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return Point{}, fmt.Errorf("want lat,lon, got %q", s)
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Point{}, fmt.Errorf("bad position %q", s)
	}
	return Point{lat, lon}, nil
}

const earthM = 6371008.8 // mean earth radius

// Offset returns the point at a distance (m) and bearing (degrees from north).
// For distances of a few hundred metres a flat earth is exact enough.
func (p Point) Offset(m, bearing float64) Point {
	b := bearing * math.Pi / 180
	dLat := m * math.Cos(b) / earthM
	dLon := m * math.Sin(b) / (earthM * math.Cos(p.Lat*math.Pi/180))
	return Point{p.Lat + dLat*180/math.Pi, p.Lon + dLon*180/math.Pi}
}

// Circle returns n+1 points (closed ring) at radius m around p, as [lon, lat].
func (p Point) Circle(m float64, n int) [][2]float64 {
	out := make([][2]float64, 0, n+1)
	for i := 0; i <= n; i++ {
		q := p.Offset(m, float64(i)*360/float64(n))
		out = append(out, [2]float64{q.Lon, q.Lat})
	}
	return out
}

// Model is the path-loss model for one radio kind.
type Model struct {
	Ref   float64 // dBm at 1 m
	N     float64 // usual exponent
	NNear float64 // largest exponent: gives the near edge of the band
	NFar  float64 // smallest exponent: gives the far edge of the band
	MaxM  float64 // no distance beyond this
}

// Range returns the near, usual and far distance in metres for a level in dBm.
func (m Model) Range(rssi float64) (near, est, far float64) {
	d := func(n float64) float64 {
		v := math.Pow(10, (m.Ref-rssi)/(10*n))
		return math.Min(math.Max(v, 1), m.MaxM)
	}
	return d(m.NNear), d(m.N), d(m.NFar)
}

// Bearing gives a fixed made-up direction (degrees) for a radio id.
func Bearing(id string) float64 {
	h := fnv.New32a()
	h.Write([]byte(id))
	return float64(h.Sum32()%3600) / 10
}

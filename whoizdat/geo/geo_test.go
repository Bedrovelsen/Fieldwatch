package geo

import (
	"math"
	"testing"
)

func TestOffset(t *testing.T) {
	p := Point{45.4215, -75.6972} // Ottawa
	q := p.Offset(100, 90)
	// 100 m east: latitude stays, longitude grows by about 100 / (111320 cos 45.42)
	if math.Abs(q.Lat-p.Lat) > 1e-9 || math.Abs((q.Lon-p.Lon)-100/(111195*math.Cos(p.Lat*math.Pi/180))) > 1e-6 {
		t.Errorf("east: %+v", q)
	}
	q = p.Offset(1000, 0)
	if d := (q.Lat - p.Lat) * 111195; math.Abs(d-1000) > 1 {
		t.Errorf("north 1000 m gave %.1f m", d)
	}
	ring := p.Circle(50, 36)
	if len(ring) != 37 || ring[0] != ring[36] {
		t.Errorf("ring not closed: %d", len(ring))
	}
}

func TestRange(t *testing.T) {
	m := Model{Ref: -40, N: 2.7, NNear: 3.5, NFar: 2.0, MaxM: 400}
	near, est, far := m.Range(-70)
	if !(near < est && est < far) || math.Abs(est-12.9) > 0.1 || math.Abs(far-31.6) > 0.1 {
		t.Errorf("range %.1f %.1f %.1f", near, est, far)
	}
	near, est, far = m.Range(-30)
	if near != 1 || est != 1 || far != 1 {
		t.Errorf("stronger than the 1 m level must give 1 m: %.1f %.1f %.1f", near, est, far)
	}
	if _, _, far := m.Range(-120); far != 400 {
		t.Errorf("far limit: %.1f", far)
	}
}

func TestParseAndBearing(t *testing.T) {
	if p, err := ParsePoint(" 45.4215, -75.6972 "); err != nil || p.Lat != 45.4215 {
		t.Errorf("%v %v", p, err)
	}
	for _, s := range []string{"45", "x,y", "95,0"} {
		if _, err := ParsePoint(s); err == nil {
			t.Errorf("ParsePoint(%q) must fail", s)
		}
	}
	if Bearing("a") != Bearing("a") || Bearing("a") == Bearing("b") || Bearing("a") < 0 || Bearing("a") >= 360 {
		t.Error("bearing must be fixed per id and in [0, 360)")
	}
}

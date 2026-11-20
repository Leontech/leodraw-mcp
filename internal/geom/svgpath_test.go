package geom

import (
	"math"
	"testing"
)

func near(a, b Point) bool { return math.Abs(a.X-b.X) < 1e-6 && math.Abs(a.Y-b.Y) < 1e-6 }

func TestParseSVGPathBasic(t *testing.T) {
	sp, err := ParseSVGPath("M10 10 h20 v20 H10 z m5,5 l5-5 L30 30")
	if err != nil {
		t.Fatal(err)
	}
	if len(sp) != 2 {
		t.Fatalf("subpaths = %d", len(sp))
	}
	if !sp[0].Closed || len(sp[0].Segments) != 3 || !near(sp[0].Segments[1].To, Point{30, 30}) {
		t.Errorf("first: %+v", sp[0])
	}
	// after z the current point is the start (10,10); m5,5 → (15,15)
	if !near(sp[1].Start, Point{15, 15}) || !near(sp[1].Segments[0].To, Point{20, 10}) {
		t.Errorf("second: %+v", sp[1])
	}
}

func TestParseSVGPathImplicitLineto(t *testing.T) {
	sp, err := ParseSVGPath("M0,0 10,0 10,10")
	if err != nil {
		t.Fatal(err)
	}
	if len(sp[0].Segments) != 2 || !near(sp[0].Segments[1].To, Point{10, 10}) {
		t.Errorf("%+v", sp)
	}
}

func TestParseSVGPathCurves(t *testing.T) {
	sp, err := ParseSVGPath("M0 0 C 0 10 10 10 10 0 S 20 -10 20 0 Q 25 10 30 0 T 40 0")
	if err != nil {
		t.Fatal(err)
	}
	s := sp[0].Segments
	if len(s) != 4 {
		t.Fatalf("segments = %d", len(s))
	}
	if !near(s[1].C1, Point{10, -10}) { // reflection of (10,10) around (10,0)
		t.Errorf("S reflection: %+v", s[1].C1)
	}
	if !near(s[2].C1, Point{20 + 2.0/3*5, 2.0 / 3 * 10}) {
		t.Errorf("Q conversion: %+v", s[2].C1)
	}
	if !near(s[3].To, Point{40, 0}) {
		t.Errorf("T end: %+v", s[3].To)
	}
}

func TestParseSVGPathArc(t *testing.T) {
	// half circle radius 10 from (0,0) to (20,0)
	sp, err := ParseSVGPath("M0 0 A10 10 0 0 1 20 0")
	if err != nil {
		t.Fatal(err)
	}
	segs := sp[0].Segments
	if len(segs) != 2 || !near(segs[1].To, Point{20, 0}) {
		t.Fatalf("arc: %+v", segs)
	}
	// midpoint of the arc should be (10,-10) or (10,10)
	if m := segs[0].To; math.Abs(m.X-10) > 1e-6 || math.Abs(math.Abs(m.Y)-10) > 1e-6 {
		t.Errorf("arc mid: %+v", m)
	}
	// compact flags
	if _, err := ParseSVGPath("M0 0a5 5 0 1110 0"); err != nil {
		t.Errorf("compact flags: %v", err)
	}
}

func TestParseSVGPathErrors(t *testing.T) {
	for _, d := range []string{"", "10 10", "M10", "M0 0 X 5 5"} {
		if _, err := ParseSVGPath(d); err == nil {
			t.Errorf("%q should fail", d)
		}
	}
}

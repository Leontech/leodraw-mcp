// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package geom

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Point is a 2D point.
type Point struct{ X, Y float64 }

// Segment is a line (C1, C2 unused) or a cubic Bézier segment ending at To.
type Segment struct {
	Cubic  bool
	C1, C2 Point
	To     Point
}

// SubPath is a sequence of segments starting at Start.
type SubPath struct {
	Start    Point
	Segments []Segment
	Closed   bool
}

// ParseSVGPath parses SVG path data (M L H V C S Q T A Z, absolute and
// relative) into sub-paths made of lines and cubic Béziers. Quadratic curves
// and elliptical arcs are converted to cubics.
func ParseSVGPath(d string) ([]SubPath, error) {
	p := &pathParser{s: d}
	var out []SubPath
	var cur *SubPath
	var pos, start, lastC2, lastQ Point
	var prevCmd byte
	cmd := byte(0)
	for {
		p.skipSep()
		if p.eof() {
			break
		}
		if c := p.peek(); isCmd(c) {
			cmd = c
			p.i++
		} else if cmd == 0 {
			return nil, fmt.Errorf("path data must start with a command (M), got %q", p.rest())
		} else if cmd == 'M' {
			cmd = 'L' // implicit lineto after moveto
		} else if cmd == 'm' {
			cmd = 'l'
		}
		rel := cmd >= 'a' && cmd <= 'z'
		up := cmd &^ 0x20
		abs := func(x, y float64) Point {
			if rel {
				return Point{pos.X + x, pos.Y + y}
			}
			return Point{x, y}
		}
		need := func() {
			if cur == nil {
				out = append(out, SubPath{Start: pos})
				cur = &out[len(out)-1]
			}
		}
		switch up {
		case 'M':
			v, err := p.nums(2)
			if err != nil {
				return nil, err
			}
			pos = abs(v[0], v[1])
			start = pos
			out = append(out, SubPath{Start: pos})
			cur = &out[len(out)-1]
		case 'L':
			v, err := p.nums(2)
			if err != nil {
				return nil, err
			}
			need()
			pos = abs(v[0], v[1])
			cur.Segments = append(cur.Segments, Segment{To: pos})
		case 'H':
			v, err := p.nums(1)
			if err != nil {
				return nil, err
			}
			need()
			if rel {
				pos.X += v[0]
			} else {
				pos.X = v[0]
			}
			cur.Segments = append(cur.Segments, Segment{To: pos})
		case 'V':
			v, err := p.nums(1)
			if err != nil {
				return nil, err
			}
			need()
			if rel {
				pos.Y += v[0]
			} else {
				pos.Y = v[0]
			}
			cur.Segments = append(cur.Segments, Segment{To: pos})
		case 'C':
			v, err := p.nums(6)
			if err != nil {
				return nil, err
			}
			need()
			c1, c2, to := abs(v[0], v[1]), abs(v[2], v[3]), abs(v[4], v[5])
			cur.Segments = append(cur.Segments, Segment{Cubic: true, C1: c1, C2: c2, To: to})
			pos, lastC2 = to, c2
		case 'S':
			v, err := p.nums(4)
			if err != nil {
				return nil, err
			}
			need()
			c1 := pos
			if pu := prevCmd &^ 0x20; pu == 'C' || pu == 'S' {
				c1 = Point{2*pos.X - lastC2.X, 2*pos.Y - lastC2.Y}
			}
			c2, to := abs(v[0], v[1]), abs(v[2], v[3])
			cur.Segments = append(cur.Segments, Segment{Cubic: true, C1: c1, C2: c2, To: to})
			pos, lastC2 = to, c2
		case 'Q', 'T':
			var q, to Point
			if up == 'Q' {
				v, err := p.nums(4)
				if err != nil {
					return nil, err
				}
				q, to = abs(v[0], v[1]), abs(v[2], v[3])
			} else {
				v, err := p.nums(2)
				if err != nil {
					return nil, err
				}
				q = pos
				if pu := prevCmd &^ 0x20; pu == 'Q' || pu == 'T' {
					q = Point{2*pos.X - lastQ.X, 2*pos.Y - lastQ.Y}
				}
				to = abs(v[0], v[1])
			}
			need()
			c1 := Point{pos.X + 2.0/3*(q.X-pos.X), pos.Y + 2.0/3*(q.Y-pos.Y)}
			c2 := Point{to.X + 2.0/3*(q.X-to.X), to.Y + 2.0/3*(q.Y-to.Y)}
			cur.Segments = append(cur.Segments, Segment{Cubic: true, C1: c1, C2: c2, To: to})
			pos, lastQ = to, q
		case 'A':
			v, err := p.arcNums()
			if err != nil {
				return nil, err
			}
			need()
			to := abs(v[5], v[6])
			cur.Segments = append(cur.Segments, arcToCubics(pos, v[0], v[1], v[2], v[3] != 0, v[4] != 0, to)...)
			pos = to
		case 'Z':
			if cur != nil {
				cur.Closed = true
			}
			pos = start
			cur = nil
		default:
			return nil, fmt.Errorf("unsupported path command %q", cmd)
		}
		prevCmd = cmd
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	return out, nil
}

func isCmd(c byte) bool { return strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", c) >= 0 }

type pathParser struct {
	s string
	i int
}

func (p *pathParser) eof() bool    { return p.i >= len(p.s) }
func (p *pathParser) peek() byte   { return p.s[p.i] }
func (p *pathParser) rest() string { return p.s[p.i:] }

func (p *pathParser) skipSep() {
	for !p.eof() {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r', ',':
			p.i++
		default:
			return
		}
	}
}

func (p *pathParser) num() (float64, error) {
	p.skipSep()
	st := p.i
	if !p.eof() && (p.s[p.i] == '+' || p.s[p.i] == '-') {
		p.i++
	}
	dot, exp := false, false
	for !p.eof() {
		c := p.s[p.i]
		switch {
		case c >= '0' && c <= '9':
		case c == '.' && !dot && !exp:
			dot = true
		case (c == 'e' || c == 'E') && !exp:
			exp = true
			if p.i+1 < len(p.s) && (p.s[p.i+1] == '+' || p.s[p.i+1] == '-') {
				p.i++
			}
		default:
			goto done
		}
		p.i++
	}
done:
	if st == p.i {
		return 0, fmt.Errorf("number expected at %q", p.rest())
	}
	return strconv.ParseFloat(p.s[st:p.i], 64)
}

func (p *pathParser) nums(n int) ([]float64, error) {
	v := make([]float64, n)
	for k := range v {
		x, err := p.num()
		if err != nil {
			return nil, err
		}
		v[k] = x
	}
	return v, nil
}

// arcNums reads rx ry rotation large-arc sweep x y; the flags may be written
// without separators ("a1 1 0 011 1").
func (p *pathParser) arcNums() ([]float64, error) {
	v := make([]float64, 7)
	for k := 0; k < 7; k++ {
		if k == 3 || k == 4 {
			p.skipSep()
			if p.eof() || (p.s[p.i] != '0' && p.s[p.i] != '1') {
				return nil, fmt.Errorf("arc flag expected at %q", p.rest())
			}
			v[k] = float64(p.s[p.i] - '0')
			p.i++
			continue
		}
		x, err := p.num()
		if err != nil {
			return nil, err
		}
		v[k] = x
	}
	return v, nil
}

// arcToCubics converts an SVG elliptical arc to cubic Bézier segments.
func arcToCubics(p0 Point, rx, ry, phiDeg float64, large, sweep bool, p1 Point) []Segment {
	if rx == 0 || ry == 0 || (p0 == p1) {
		return []Segment{{To: p1}}
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := phiDeg * math.Pi / 180
	cos, sin := math.Cos(phi), math.Sin(phi)
	dx, dy := (p0.X-p1.X)/2, (p0.Y-p1.Y)/2
	x1p := cos*dx + sin*dy
	y1p := -sin*dx + cos*dy
	lam := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry)
	if lam > 1 {
		s := math.Sqrt(lam)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := 0.0
	if den != 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp, cyp := coef*rx*y1p/ry, -coef*ry*x1p/rx
	cx := cos*cxp - sin*cyp + (p0.X+p1.X)/2
	cy := sin*cxp + cos*cyp + (p0.Y+p1.Y)/2
	ang := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	t1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dt := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	} else if sweep && dt < 0 {
		dt += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dt) / (math.Pi / 2)))
	if n < 1 {
		n = 1
	}
	step := dt / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	pt := func(t float64) (Point, Point) { // point and derivative direction
		ct, st := math.Cos(t), math.Sin(t)
		x := cx + rx*ct*cos - ry*st*sin
		y := cy + rx*ct*sin + ry*st*cos
		dx := -rx*st*cos - ry*ct*sin
		dy := -rx*st*sin + ry*ct*cos
		return Point{x, y}, Point{dx, dy}
	}
	segs := make([]Segment, 0, n)
	t := t1
	for i := 0; i < n; i++ {
		a, da := pt(t)
		b, db := pt(t + step)
		if i == n-1 {
			b = p1
		}
		segs = append(segs, Segment{Cubic: true,
			C1: Point{a.X + k*da.X, a.Y + k*da.Y},
			C2: Point{b.X - k*db.X, b.Y - k*db.Y},
			To: b})
		t += step
	}
	return segs
}

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package qr encodes QR codes (ISO/IEC 18004, model 2, versions 1–40) using
// numeric, alphanumeric or byte mode for the whole text.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Level is the error correction level.
type Level int

const (
	L Level = iota // ~7 %
	M              // ~15 %
	Q              // ~25 %
	H              // ~30 %
)

// ParseLevel parses "L", "M", "Q" or "H" (default M for "").
func ParseLevel(s string) (Level, error) {
	switch strings.ToUpper(s) {
	case "L":
		return L, nil
	case "", "M":
		return M, nil
	case "Q":
		return Q, nil
	case "H":
		return H, nil
	}
	return M, fmt.Errorf("unknown error correction level %q (L, M, Q, H)", s)
}

var formatBitsOf = [4]int{1, 0, 3, 2} // L M Q H

var eccPerBlock = [4][41]int{
	{-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28},
	{-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	{-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
}

var numBlocks = [4][41]int{
	{-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25},
	{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49},
	{-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68},
	{-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81},
}

// Mode is the data encoding mode.
type Mode int

const (
	Numeric Mode = iota
	Alphanumeric
	Byte
)

func (m Mode) String() string { return [...]string{"numeric", "alphanumeric", "byte"}[m] }

const alnum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

// Code is an encoded QR code. Modules[y][x] is true for dark modules.
type Code struct {
	Version int
	Level   Level
	Mask    int
	Mode    Mode
	Size    int
	Modules [][]bool
}

// Options for Encode.
type Options struct {
	Level      Level
	MinVersion int // 0 = 1
	Mask       int // -1 = automatic (lowest penalty)
}

// Encode encodes text with the most compact mode and the smallest version.
func Encode(text string, opt Options) (*Code, error) {
	if text == "" {
		return nil, errors.New("qr: empty text")
	}
	mode := Byte
	if isNumeric(text) {
		mode = Numeric
	} else if isAlnum(text) {
		mode = Alphanumeric
	}
	data := []byte(text)
	minV := opt.MinVersion
	if minV < 1 {
		minV = 1
	}
	for v := minV; v <= 40; v++ {
		capBits := numDataCodewords(v, opt.Level) * 8
		bits, ok := encodeSegment(mode, text, data, v)
		if !ok || len(bits) > capBits {
			continue
		}
		return build(bits, v, opt.Level, opt.Mask, mode)
	}
	return nil, fmt.Errorf("qr: text too long (%d bytes) for error correction level %s", len(data), "LMQH"[opt.Level:opt.Level+1])
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isAlnum(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune(alnum, r) {
			return false
		}
	}
	return true
}

func numRawDataModules(v int) int {
	r := (16*v+128)*v + 64
	if v >= 2 {
		na := v/7 + 2
		r -= (25*na-10)*na - 55
		if v >= 7 {
			r -= 36
		}
	}
	return r
}

func numDataCodewords(v int, l Level) int {
	return numRawDataModules(v)/8 - eccPerBlock[l][v]*numBlocks[l][v]
}

type bitBuf []bool

func (b *bitBuf) append(val uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, (val>>uint(i))&1 != 0)
	}
}

func countBits(m Mode, v int) int {
	idx := 0
	if v >= 10 {
		idx = 1
	}
	if v >= 27 {
		idx = 2
	}
	return [3][3]int{{10, 12, 14}, {9, 11, 13}, {8, 16, 16}}[m][idx]
}

// encodeSegment returns mode indicator + count + data bits.
func encodeSegment(m Mode, text string, data []byte, v int) (bitBuf, bool) {
	var b bitBuf
	cb := countBits(m, v)
	switch m {
	case Numeric:
		if len(text) >= 1<<cb {
			return nil, false
		}
		b.append(1, 4)
		b.append(uint32(len(text)), cb)
		for i := 0; i < len(text); i += 3 {
			n := min(3, len(text)-i)
			var val uint32
			for _, c := range text[i : i+n] {
				val = val*10 + uint32(c-'0')
			}
			b.append(val, n*3+1)
		}
	case Alphanumeric:
		if len(text) >= 1<<cb {
			return nil, false
		}
		b.append(2, 4)
		b.append(uint32(len(text)), cb)
		for i := 0; i+1 < len(text); i += 2 {
			b.append(uint32(strings.IndexByte(alnum, text[i])*45+strings.IndexByte(alnum, text[i+1])), 11)
		}
		if len(text)%2 == 1 {
			b.append(uint32(strings.IndexByte(alnum, text[len(text)-1])), 6)
		}
	default:
		if len(data) >= 1<<cb {
			return nil, false
		}
		b.append(4, 4)
		b.append(uint32(len(data)), cb)
		for _, c := range data {
			b.append(uint32(c), 8)
		}
	}
	return b, true
}

func build(bits bitBuf, v int, l Level, mask int, mode Mode) (*Code, error) {
	capBits := numDataCodewords(v, l) * 8
	bits.append(0, min(4, capBits-len(bits)))
	bits.append(0, (8-len(bits)%8)%8)
	for pad := uint32(0xEC); len(bits) < capBits; pad ^= 0xEC ^ 0x11 {
		bits.append(pad, 8)
	}
	data := make([]byte, len(bits)/8)
	for i, bit := range bits {
		if bit {
			data[i>>3] |= 1 << uint(7-i&7)
		}
	}
	all := addECC(data, v, l)

	size := v*4 + 17
	c := &Code{Version: v, Level: l, Mode: mode, Size: size}
	m := newMatrix(size)
	m.drawFunctionPatterns(v, l)
	m.drawCodewords(all)
	if mask < 0 {
		best := 1 << 30
		for k := 0; k < 8; k++ {
			m.applyMask(k)
			m.drawFormatBits(l, k)
			if p := m.penalty(); p < best {
				best, mask = p, k
			}
			m.applyMask(k) // undo (XOR)
		}
	}
	if mask > 7 {
		return nil, fmt.Errorf("qr: mask %d out of range 0–7", mask)
	}
	m.applyMask(mask)
	m.drawFormatBits(l, mask)
	c.Mask = mask
	c.Modules = m.mod
	return c, nil
}

// --- Reed–Solomon -------------------------------------------------------------

func gfMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= int((y>>uint(i))&1) * int(x)
	}
	return byte(z)
}

func rsDivisor(degree int) []byte {
	r := make([]byte, degree)
	r[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range r {
			r[j] = gfMul(r[j], root)
			if j+1 < len(r) {
				r[j] ^= r[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return r
}

func rsRemainder(data, div []byte) []byte {
	r := make([]byte, len(div))
	for _, b := range data {
		f := b ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i := range r {
			r[i] ^= gfMul(div[i], f)
		}
	}
	return r
}

func addECC(data []byte, v int, l Level) []byte {
	nb := numBlocks[l][v]
	eccLen := eccPerBlock[l][v]
	raw := numRawDataModules(v) / 8
	numShort := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(eccLen)
	var blocks [][]byte
	k := 0
	for i := 0; i < nb; i++ {
		datLen := shortLen - eccLen
		if i >= numShort {
			datLen++
		}
		dat := append([]byte{}, data[k:k+datLen]...)
		k += datLen
		ecc := rsRemainder(dat, div)
		if i < numShort {
			dat = append(dat, 0)
		}
		blocks = append(blocks, append(dat, ecc...))
	}
	var out []byte
	for i := range blocks[0] {
		for j, b := range blocks {
			if i != shortLen-eccLen || j >= numShort {
				out = append(out, b[i])
			}
		}
	}
	return out
}

// --- matrix -------------------------------------------------------------------

type matrix struct {
	size int
	mod  [][]bool
	fn   [][]bool
}

func newMatrix(size int) *matrix {
	m := &matrix{size: size, mod: make([][]bool, size), fn: make([][]bool, size)}
	for i := range m.mod {
		m.mod[i] = make([]bool, size)
		m.fn[i] = make([]bool, size)
	}
	return m
}

func (m *matrix) setFn(x, y int, dark bool) {
	m.mod[y][x] = dark
	m.fn[y][x] = true
}

func (m *matrix) drawFunctionPatterns(v int, l Level) {
	for i := 0; i < m.size; i++ {
		m.setFn(6, i, i%2 == 0)
		m.setFn(i, 6, i%2 == 0)
	}
	m.drawFinder(3, 3)
	m.drawFinder(m.size-4, 3)
	m.drawFinder(3, m.size-4)
	pos := alignmentPositions(v)
	n := len(pos)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if (i == 0 && j == 0) || (i == 0 && j == n-1) || (i == n-1 && j == 0) {
				continue
			}
			m.drawAlignment(pos[i], pos[j])
		}
	}
	m.drawFormatBits(l, 0) // reserve
	m.drawVersion(v)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (m *matrix) drawFinder(x, y int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			dist := max(abs(dx), abs(dy))
			xx, yy := x+dx, y+dy
			if xx >= 0 && xx < m.size && yy >= 0 && yy < m.size {
				m.setFn(xx, yy, dist != 2 && dist != 4)
			}
		}
	}
}

func (m *matrix) drawAlignment(x, y int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			m.setFn(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

func alignmentPositions(v int) []int {
	if v == 1 {
		return nil
	}
	na := v/7 + 2
	step := 26
	if v != 32 {
		step = (v*4 + na*2 + 1) / (na*2 - 2) * 2
	}
	res := make([]int, na)
	res[0] = 6
	for i, pos := na-1, v*4+17-7; i >= 1; i, pos = i-1, pos-step {
		res[i] = pos
	}
	return res
}

func (m *matrix) drawFormatBits(l Level, mask int) {
	data := formatBitsOf[l]<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>uint(i))&1 != 0 }
	for i := 0; i <= 5; i++ {
		m.setFn(8, i, bit(i))
	}
	m.setFn(8, 7, bit(6))
	m.setFn(8, 8, bit(7))
	m.setFn(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.setFn(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		m.setFn(m.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.setFn(8, m.size-15+i, bit(i))
	}
	m.setFn(8, m.size-8, true)
}

func (m *matrix) drawVersion(v int) {
	if v < 7 {
		return
	}
	rem := v
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
	}
	bits := v<<12 | rem
	for i := 0; i < 18; i++ {
		b := (bits>>uint(i))&1 != 0
		a := m.size - 11 + i%3
		c := i / 3
		m.setFn(a, c, b)
		m.setFn(c, a, b)
	}
}

func (m *matrix) drawCodewords(data []byte) {
	i := 0
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = m.size - 1 - vert
				}
				if !m.fn[y][x] && i < len(data)*8 {
					m.mod[y][x] = (data[i>>3]>>uint(7-i&7))&1 != 0
					i++
				}
			}
		}
	}
}

func (m *matrix) applyMask(k int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			var inv bool
			switch k {
			case 0:
				inv = (x+y)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (x+y)%3 == 0
			case 4:
				inv = (x/3+y/2)%2 == 0
			case 5:
				inv = x*y%2+x*y%3 == 0
			case 6:
				inv = (x*y%2+x*y%3)%2 == 0
			case 7:
				inv = ((x+y)%2+x*y%3)%2 == 0
			}
			if inv && !m.fn[y][x] {
				m.mod[y][x] = !m.mod[y][x]
			}
		}
	}
}

// penalty computes the mask penalty score (ISO/IEC 18004 §7.8.3).
func (m *matrix) penalty() int {
	n := m.size
	p := 0
	get := func(x, y int, col bool) bool {
		if col {
			return m.mod[x][y]
		}
		return m.mod[y][x]
	}
	for _, col := range []bool{false, true} {
		for y := 0; y < n; y++ {
			run := 1
			for x := 1; x < n; x++ {
				if get(x, y, col) == get(x-1, y, col) {
					run++
					continue
				}
				if run >= 5 {
					p += 3 + run - 5
				}
				run = 1
			}
			if run >= 5 {
				p += 3 + run - 5
			}
			// finder-like pattern 1:1:3:1:1 with 4 light modules on one side
			for x := 0; x+11 <= n+4; x++ {
				if finderAt(get, x, y, n, col) {
					p += 40
				}
			}
		}
	}
	for y := 0; y+1 < n; y++ {
		for x := 0; x+1 < n; x++ {
			c := m.mod[y][x]
			if c == m.mod[y][x+1] && c == m.mod[y+1][x] && c == m.mod[y+1][x+1] {
				p += 3
			}
		}
	}
	dark := 0
	for _, row := range m.mod {
		for _, d := range row {
			if d {
				dark++
			}
		}
	}
	total := n * n
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return p + k*10
}

var finderPat = [7]bool{true, false, true, true, true, false, true}

// finderAt checks for 1011101 at x..x+6 preceded or followed by 4 light
// modules (outside the symbol counts as light).
func finderAt(get func(x, y int, col bool) bool, x, y, n int, col bool) bool {
	if x+7 > n {
		return false
	}
	for i, want := range finderPat {
		if get(x+i, y, col) != want {
			return false
		}
	}
	light := func(from, to int) bool {
		for i := from; i < to; i++ {
			if i >= 0 && i < n && get(i, y, col) {
				return false
			}
		}
		return true
	}
	return light(x-4, x) || light(x+7, x+11)
}

// PathData returns SVG path data (in modules) drawing the dark modules as
// rectangles merged per row, offset by quiet modules.
func (c *Code) PathData(quiet int) string {
	var b strings.Builder
	for y, row := range c.Modules {
		for x := 0; x < c.Size; {
			if !row[x] {
				x++
				continue
			}
			x0 := x
			for x < c.Size && row[x] {
				x++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x0+quiet, y+quiet, x-x0, x-x0)
		}
	}
	return b.String()
}

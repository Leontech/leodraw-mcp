// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package geom converts between LeoDraw page coordinates and CorelDRAW
// document coordinates.
//
// LeoDraw coordinates are millimetres relative to the page, with the origin
// at the top-left corner of the page and Y growing downwards (like SVG and
// the PNG preview). CorelDRAW document coordinates usually have Y growing
// upwards; the direction is detected from the page edges.
package geom

import "math"

// Frame describes a page in CorelDRAW document coordinates (millimetres).
type Frame struct {
	Left   float64 // x of the page's left edge (Corel)
	Top    float64 // y of the page's top edge (Corel)
	YUp    bool    // true when Corel's y grows upwards (TopY > BottomY)
	Width  float64 // page width
	Height float64 // page height
}

// NewFrame builds a frame from the page edges as returned by CorelDRAW.
func NewFrame(leftX, topY, bottomY, width, height float64) Frame {
	return Frame{Left: leftX, Top: topY, YUp: topY >= bottomY, Width: width, Height: height}
}

// ToPage converts a Corel point to page coordinates.
func (f Frame) ToPage(xc, yc float64) (x, y float64) {
	x = xc - f.Left
	if f.YUp {
		y = f.Top - yc
	} else {
		y = yc - f.Top
	}
	return x, y
}

// FromPage converts a page point to Corel coordinates.
func (f Frame) FromPage(x, y float64) (xc, yc float64) {
	xc = x + f.Left
	if f.YUp {
		yc = f.Top - y
	} else {
		yc = f.Top + y
	}
	return xc, yc
}

// DeltaFromPage converts a page-space displacement to a Corel displacement.
func (f Frame) DeltaFromPage(dx, dy float64) (dxc, dyc float64) {
	if f.YUp {
		return dx, -dy
	}
	return dx, dy
}

// Round rounds to 0.01 (enough for mm output and keeps JSON short).
func Round(v float64) float64 {
	r := math.Round(v*100) / 100
	if r == 0 {
		return 0 // avoid -0
	}
	return r
}

// Rect is an axis-aligned rectangle in page coordinates.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Rounded returns r with all values rounded by Round.
func (r Rect) Rounded() Rect {
	return Rect{Round(r.X), Round(r.Y), Round(r.W), Round(r.H)}
}

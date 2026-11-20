// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package geom

import "testing"

func TestFrameYUp(t *testing.T) {
	// A4 page, Corel origin at the bottom-left corner of the page.
	f := NewFrame(0, 297, 0, 210, 297)
	if !f.YUp {
		t.Fatal("expected YUp")
	}
	x, y := f.ToPage(10, 287)
	if x != 10 || y != 10 {
		t.Errorf("ToPage = %v,%v", x, y)
	}
	xc, yc := f.FromPage(10, 10)
	if xc != 10 || yc != 287 {
		t.Errorf("FromPage = %v,%v", xc, yc)
	}
	if dx, dy := f.DeltaFromPage(1, 2); dx != 1 || dy != -2 {
		t.Errorf("Delta = %v,%v", dx, dy)
	}
}

func TestFrameCentered(t *testing.T) {
	// Origin in the page centre (some Corel setups).
	f := NewFrame(-105, 148.5, -148.5, 210, 297)
	x, y := f.ToPage(-105, 148.5)
	if x != 0 || y != 0 {
		t.Errorf("top-left = %v,%v", x, y)
	}
	xc, yc := f.FromPage(210, 297)
	if xc != 105 || yc != -148.5 {
		t.Errorf("bottom-right = %v,%v", xc, yc)
	}
}

func TestFrameYDown(t *testing.T) {
	f := NewFrame(0, 0, 297, 210, 297)
	if f.YUp {
		t.Fatal("expected Y down")
	}
	xc, yc := f.FromPage(5, 7)
	if xc != 5 || yc != 7 {
		t.Errorf("FromPage = %v,%v", xc, yc)
	}
	if x, y := f.ToPage(xc, yc); x != 5 || y != 7 {
		t.Errorf("round trip = %v,%v", x, y)
	}
}

func TestRound(t *testing.T) {
	if Round(1.23456) != 1.23 || Round(-0.001) != 0 {
		t.Error("Round")
	}
}

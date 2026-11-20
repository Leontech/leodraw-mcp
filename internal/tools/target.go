//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

// targetArgs select the shapes a tool works on.
type targetArgs struct {
	IDs       []int64 `json:"ids"`
	Selection bool    `json:"selection"`
	Page      int     `json:"page"`
}

func targetSchema(props map[string]mcp.Schema) map[string]mcp.Schema {
	props["ids"] = mcp.Array("Shape ids (from list_shapes or creation tools).", mcp.Integer("shape id"))
	props["selection"] = mcp.Boolean("Use the shapes currently selected in CorelDRAW instead of ids.")
	props["page"] = mcp.Integer("Page number (1-based) the ids are on. Default: the active page.")
	return props
}

// pageCtx is what most tools need: document, page and its frame (units are mm).
type pageCtx struct {
	c     *corel.Ctx
	doc   *com.Dispatch
	page  *com.Dispatch
	index int
	f     geom.Frame
}

// withPage resolves the document and page, sets mm units and runs fn – as one
// undo step named opName when opName is not empty.
func withPage(c *corel.Ctx, pageIndex int, opName string, fn func(p *pageCtx) error) error {
	doc, err := c.ActiveDocument()
	if err != nil {
		return err
	}
	return c.WithMM(doc, func() error {
		page, idx, err := c.Page(doc, pageIndex)
		if err != nil {
			return err
		}
		f, err := c.Frame(page)
		if err != nil {
			return err
		}
		p := &pageCtx{c: c, doc: doc, page: page, index: idx, f: f}
		if opName == "" {
			return fn(p)
		}
		// Some operations (Group, Align, Combine, selections …) silently do
		// nothing on a page that is not active.
		if _, active, err := c.Page(doc, 0); err == nil && active != idx {
			if _, err := page.Call("Activate"); err != nil {
				return err
			}
		}
		return c.Write(doc, opName, func() error { return fn(p) })
	})
}

// shapes returns a ShapeRange with the target shapes and the shapes themselves.
func (p *pageCtx) shapes(t *targetArgs) (*com.Dispatch, []*com.Dispatch, error) {
	switch {
	case len(t.IDs) > 0:
		sr, err := p.c.App.CallObj("CreateShapeRange")
		if err != nil {
			return nil, nil, err
		}
		var list []*com.Dispatch
		for _, id := range t.IDs {
			s, err := corel.FindShape(p.page, id)
			if err != nil {
				return nil, nil, err
			}
			if _, err := sr.Call("Add", s); err != nil {
				return nil, nil, err
			}
			list = append(list, s)
		}
		return sr, list, nil
	case t.Selection:
		sr, err := p.doc.GetObj("SelectionRange")
		if err != nil {
			return nil, nil, err
		}
		items, n, err := corel.Collect(sr, 0)
		if err != nil {
			return nil, nil, err
		}
		if n == 0 {
			return nil, nil, errors.New("nothing is selected in CorelDRAW")
		}
		return sr, items, nil
	}
	return nil, nil, errors.New("specify the shapes: ids (list) or selection: true")
}

// describe returns ShapeInfo for shapes.
func (p *pageCtx) describe(list []*com.Dispatch) []corel.ShapeInfo {
	out := make([]corel.ShapeInfo, 0, len(list))
	for _, s := range list {
		if si, err := p.c.DescribeShape(s, p.f, "", 0); err == nil {
			out = append(out, si)
		}
	}
	return out
}

// rangeList returns the shapes of a ShapeRange.
func rangeList(sr *com.Dispatch) []*com.Dispatch {
	if sr == nil {
		return nil
	}
	items, _, _ := corel.Collect(sr, 0)
	return items
}

// center returns the centre of a shape / range in Corel coordinates.
func center(obj *com.Dispatch) (float64, float64, error) {
	cx, err := obj.GetFloat("CenterX")
	if err != nil {
		return 0, 0, err
	}
	cy, err := obj.GetFloat("CenterY")
	return cx, cy, err
}

// cdrBool converts to cdrTriState (-1 true, 0 false).
func cdrBool(b bool) int {
	if b {
		return -1
	}
	return 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

// anchors maps an anchor name to fractions of the bounding box (0 = left/top, 1 = right/bottom).
var anchors = map[string][2]float64{
	"top-left": {0, 0}, "top": {0.5, 0}, "top-right": {1, 0},
	"left": {0, 0.5}, "center": {0.5, 0.5}, "right": {1, 0.5},
	"bottom-left": {0, 1}, "bottom": {0.5, 1}, "bottom-right": {1, 1},
}

var anchorNames = []string{"top-left", "top", "top-right", "left", "center", "right", "bottom-left", "bottom", "bottom-right"}

func anchorSchema() mcp.Schema {
	return mcp.Enum("Which point of the bounding box x/y refers to. Default top-left.", anchorNames...)
}

func anchorFrac(name string) ([2]float64, error) {
	if name == "" {
		return anchors["top-left"], nil
	}
	a, ok := anchors[name]
	if !ok {
		return a, fmt.Errorf("unknown anchor %q (use one of %v)", name, anchorNames)
	}
	return a, nil
}

// anchorPoint returns the page position of an anchor of obj's bounding box.
func anchorPoint(obj *com.Dispatch, f geom.Frame, anchor string) (float64, float64, error) {
	a, err := anchorFrac(anchor)
	if err != nil {
		return 0, 0, err
	}
	bb, err := corel.BBox(obj, f)
	if err != nil {
		return 0, 0, err
	}
	return bb.X + a[0]*bb.W, bb.Y + a[1]*bb.H, nil
}

// placeAt moves obj (shape or range) so that its anchor point lies at page (x, y).
func placeAt(obj *com.Dispatch, f geom.Frame, x, y float64, anchor string) error {
	px, py, err := anchorPoint(obj, f, anchor)
	if err != nil {
		return err
	}
	return moveBy(obj, f, x-px, y-py)
}

// placeAtOpt moves obj so that its anchor point lies at (x, y); a nil
// coordinate keeps that axis unchanged.
func placeAtOpt(obj *com.Dispatch, f geom.Frame, x, y *float64, anchor string) error {
	if x == nil && y == nil {
		return nil
	}
	px, py, err := anchorPoint(obj, f, anchor)
	if err != nil {
		return err
	}
	tx, ty := px, py
	if x != nil {
		tx = *x
	}
	if y != nil {
		ty = *y
	}
	return moveBy(obj, f, tx-px, ty-py)
}

// moveBy moves obj by a page-space displacement.
func moveBy(obj *com.Dispatch, f geom.Frame, dx, dy float64) error {
	if math.Abs(dx) < 1e-9 && math.Abs(dy) < 1e-9 {
		return nil
	}
	dxc, dyc := f.DeltaFromPage(dx, dy)
	_, err := obj.Call("Move", dxc, dyc)
	return err
}

// walkShapes calls fn for every shape in shapes, descending into groups up to maxDepth.
// fn returns false to stop the walk.
func walkShapes(shapes *com.Dispatch, depth, maxDepth int, fn func(s *com.Dispatch, depth int) bool) bool {
	items, _, err := corel.Collect(shapes, 0)
	if err != nil {
		return true
	}
	for _, s := range items {
		if !fn(s, depth) {
			return false
		}
		if depth < maxDepth {
			if t, _ := s.GetInt("Type"); t == 7 {
				if sub, err := s.GetObj("Shapes"); err == nil {
					if !walkShapes(sub, depth+1, maxDepth, fn) {
						return false
					}
				}
			}
		}
	}
	return true
}

// newRange builds a ShapeRange from shapes.
func newRange(c *corel.Ctx, list []*com.Dispatch) (*com.Dispatch, error) {
	sr, err := c.App.CallObj("CreateShapeRange")
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if _, err := sr.Call("Add", s); err != nil {
			return nil, err
		}
	}
	return sr, nil
}

// ptr helpers for optional JSON values
func f64(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type getShapeArgs struct {
	ID   int64 `json:"id"`
	Page int   `json:"page"`
}

type findShapesArgs struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Text     string   `json:"text"`
	Layer    string   `json:"layer"`
	Fill     string   `json:"fill"`
	Page     int      `json:"page"`
	AllPages bool     `json:"all_pages"`
	Groups   bool     `json:"inside_groups"`
	Limit    int      `json:"limit"`
	X        *float64 `json:"x"`
	Y        *float64 `json:"y"`
	W        *float64 `json:"w"`
	H        *float64 `json:"h"`
	Touch    bool     `json:"touch"`
	Compact  bool     `json:"compact"`
}

type getPathArgs struct {
	ID       int64 `json:"id"`
	Page     int   `json:"page"`
	MaxNodes int   `json:"max_nodes"`
}

type selectArgs struct {
	IDs  []int64 `json:"ids"`
	Page int     `json:"page"`
	Add  bool    `json:"add"`
}

type shapeDetail struct {
	corel.ShapeInfo
	Page         int      `json:"page"`
	Rotation     float64  `json:"rotation,omitempty"`
	Locked       bool     `json:"locked,omitempty"`
	ZOrder       int64    `json:"z_order,omitempty"`
	OutlineWidth *float64 `json:"outline_width_mm,omitempty"`
	Nodes        int64    `json:"nodes,omitempty"`
	Subpaths     int64    `json:"subpaths,omitempty"`
	Closed       *bool    `json:"closed,omitempty"`
	Font         string   `json:"font,omitempty"`
	FontSize     float64  `json:"font_size_pt,omitempty"`
	Bold         bool     `json:"bold,omitempty"`
	Italic       bool     `json:"italic,omitempty"`
	Align        string   `json:"align,omitempty"`
	TextKind     string   `json:"text_kind,omitempty"`
	Overflow     bool     `json:"text_overflow,omitempty"`
}

type foundShape struct {
	corel.ShapeInfo
	Page int `json:"page"`
}

func inspect2Tools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "get_shape",
			Toolset: "inspect",
			Description: "Details of one shape: bounding box, fill, outline (incl. width), rotation, lock, z-order, node count of curves, " +
				"text content and font of text shapes, children of groups.",
			InputSchema: mcp.Object([]string{"id"}, map[string]mcp.Schema{
				"id":   mcp.Integer("Shape id."),
				"page": mcp.Integer("Page number. Default: the active page."),
			}),
			Annotations: readOnly("Get shape"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *getShapeArgs) (*mcp.Result, error) {
				return getShape(ctx, d, a)
			}),
		},
		{
			Name:    "find_shapes",
			Toolset: "inspect",
			Description: "Search shapes by name, type, text content, layer, fill colour and/or region (x, y, w, h in mm) on a page or " +
				"all pages. All given criteria must match; name and text match substrings (case-insensitive). " +
				"Example: everything of one label = region around it with inside_groups: true.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"name":          mcp.String("Name contains."),
				"type":          mcp.String(`Shape type, e.g. "text", "curve", "rectangle", "ellipse", "group", "bitmap".`),
				"text":          mcp.String("Text content contains (text shapes)."),
				"layer":         mcp.String("Layer name (exact, case-insensitive)."),
				"fill":          mcp.String(`Fill as reported by list_shapes, e.g. "#FF0000" or "none".`),
				"page":          mcp.Integer("Page number. Default: the active page."),
				"all_pages":     mcp.Boolean("Search all pages."),
				"inside_groups": mcp.Boolean("Search inside groups too."),
				"limit":         mcp.Default(mcp.Range(mcp.Integer("Maximum results."), 1, 2000), 200),
				"x":             mcp.Number("Region left edge (mm) – with y, w, h only shapes inside this rectangle."),
				"y":             mcp.Number("Region top edge (mm)."),
				"w":             mcp.Number("Region width (mm)."),
				"h":             mcp.Number("Region height (mm)."),
				"touch":         mcp.Boolean("Region: also shapes that only overlap it (default: fully inside)."),
				"compact":       mcp.Boolean("One short line per shape instead of JSON."),
			}),
			Annotations: readOnly("Find shapes"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *findShapesArgs) (*mcp.Result, error) {
				return findShapes(ctx, d, a)
			}),
		},
		{
			Name:    "get_path",
			Toolset: "inspect",
			Description: "Geometry of a curve shape as SVG path data (d) in page mm (origin top-left, Y down) – lets you see a shape exactly. " +
				"Other shape types (rectangle, ellipse, text …) are converted on the fly without changing the document.",
			InputSchema: mcp.Object([]string{"id"}, map[string]mcp.Schema{
				"id":        mcp.Integer("Shape id."),
				"page":      mcp.Integer("Page number. Default: the active page."),
				"max_nodes": mcp.Default(mcp.Range(mcp.Integer("Stop after this many segments."), 10, 20000), 2000),
			}),
			Annotations: readOnly("Get path"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *getPathArgs) (*mcp.Result, error) {
				return getPath(ctx, d, a)
			}),
		},
		{
			Name:        "select_shapes",
			Toolset:     "inspect",
			Description: "Select shapes in CorelDRAW (so the user sees them, or for tools working on the selection). add: keep the current selection.",
			InputSchema: mcp.Object([]string{"ids"}, map[string]mcp.Schema{
				"ids":  mcp.Array("Shape ids.", mcp.Integer("id")),
				"page": mcp.Integer("Page number. Default: the active page."),
				"add":  mcp.Boolean("Add to the current selection."),
			}),
			Annotations: modifying("Select shapes"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *selectArgs) (*mcp.Result, error) {
				return selectShapesTool(ctx, d, a)
			}),
		},
		{
			Name:        "clear_selection",
			Toolset:     "inspect",
			Description: "Deselect everything in the active document.",
			InputSchema: mcp.Object(nil, nil),
			Annotations: modifying("Clear selection"),
			Handler: handler(defaultTimeout, func(ctx context.Context, _ *struct{}) (*mcp.Result, error) {
				err := d.S.Run(ctx, func(c *corel.Ctx) error {
					doc, err := c.ActiveDocument()
					if err != nil {
						return err
					}
					_, err = doc.Call("ClearSelection")
					return err
				})
				if err != nil {
					return nil, err
				}
				return mcp.TextResult("Selection cleared."), nil
			}),
		},
	}
}

func getShape(ctx context.Context, d *Deps, a *getShapeArgs) (*mcp.Result, error) {
	var det shapeDetail
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "", func(p *pageCtx) error {
			s, err := corel.FindShape(p.page, a.ID)
			if err != nil {
				return err
			}
			layer := ""
			if l, err := s.GetObj("Layer"); err == nil {
				layer, _ = l.GetString("Name")
			}
			if det.ShapeInfo, err = c.DescribeShape(s, p.f, layer, 1); err != nil {
				return err
			}
			det.Page = p.index
			if r, err := s.GetFloat("RotationAngle"); err == nil {
				det.Rotation = geom.Round(r)
			}
			det.Locked, _ = s.GetBool("Locked")
			det.ZOrder, _ = s.GetInt("ZOrder")
			if ol, err := s.GetObj("Outline"); err == nil {
				if t, _ := ol.GetInt("Type"); t != 0 {
					if w, err := ol.GetFloat("Width"); err == nil {
						w = geom.Round(w*1000) / 1000
						det.OutlineWidth = &w
					}
				}
			}
			switch det.Type {
			case "curve":
				if cv, err := s.GetObj("Curve"); err == nil {
					if nodes, err := cv.GetObj("Nodes"); err == nil {
						det.Nodes, _ = nodes.GetInt("Count")
					}
					if sps, err := cv.GetObj("SubPaths"); err == nil {
						det.Subpaths, _ = sps.GetInt("Count")
					}
					if cl, err := cv.GetBool("Closed"); err == nil {
						det.Closed = &cl
					}
				}
			case "text":
				if txt, st, err := story(s); err == nil {
					det.Font, _ = st.GetString("Font")
					if sz, err := st.GetFloat("Size"); err == nil {
						det.FontSize = geom.Round(sz)
					}
					det.Bold, _ = st.GetBool("Bold")
					det.Italic, _ = st.GetBool("Italic")
					if al, err := st.GetInt("Alignment"); err == nil {
						det.Align = alignmentNames[al]
					}
					if art, err := txt.GetBool("IsArtisticText"); err == nil {
						det.TextKind = "paragraph"
						if art {
							det.TextKind = "artistic"
						} else {
							det.Overflow, _ = txt.GetBool("Overflow")
						}
					}
					if t, err := st.GetString("Text"); err == nil {
						det.Text = strings.ReplaceAll(t, "\r", "\n")
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Shape %d (%s).", det.ID, det.Type), det), nil
}

func findShapes(ctx context.Context, d *Deps, a *findShapesArgs) (*mcp.Result, error) {
	if a.Limit <= 0 {
		a.Limit = 200
	}
	region := a.X != nil || a.Y != nil || a.W != nil || a.H != nil
	if region && (a.X == nil || a.Y == nil || a.W == nil || a.H == nil) {
		return nil, errors.New("a region needs all of x, y, w, h")
	}
	inRegion := func(si corel.ShapeInfo) bool {
		if !region {
			return true
		}
		const eps = 0.01
		rx, ry, rw, rh := *a.X, *a.Y, *a.W, *a.H
		if a.Touch {
			return si.X <= rx+rw && si.X+si.W >= rx && si.Y <= ry+rh && si.Y+si.H >= ry
		}
		return si.X >= rx-eps && si.Y >= ry-eps && si.X+si.W <= rx+rw+eps && si.Y+si.H <= ry+rh+eps
	}
	name, text := strings.ToLower(a.Name), strings.ToLower(a.Text)
	fill := strings.ToUpper(strings.TrimSpace(a.Fill))
	var found []foundShape
	truncated := false
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		return c.WithMM(doc, func() error {
			var pages []*com.Dispatch
			if a.AllPages {
				ps, err := doc.GetObj("Pages")
				if err != nil {
					return err
				}
				if pages, _, err = corel.Collect(ps, 0); err != nil {
					return err
				}
			} else {
				pg, _, err := c.Page(doc, a.Page)
				if err != nil {
					return err
				}
				pages = []*com.Dispatch{pg}
			}
			maxDepth := 0
			if a.Groups {
				maxDepth = 20
			}
			for _, pg := range pages {
				idx, _ := pg.GetInt("Index")
				f, err := c.Frame(pg)
				if err != nil {
					return err
				}
				layers, err := pg.GetObj("Layers")
				if err != nil {
					return err
				}
				ls, _, _ := corel.Collect(layers, 0)
				for _, l := range ls {
					ln, _ := l.GetString("Name")
					if a.Layer != "" && !strings.EqualFold(ln, a.Layer) {
						continue
					}
					shapes, err := l.GetObj("Shapes")
					if err != nil {
						continue
					}
					cont := walkShapes(shapes, 0, maxDepth, func(s *com.Dispatch, _ int) bool {
						if name != "" {
							n, _ := s.GetString("Name")
							if !strings.Contains(strings.ToLower(n), name) {
								return true
							}
						}
						if a.Type != "" {
							t, _ := s.GetInt("Type")
							if corel.ShapeTypeName(t) != a.Type {
								return true
							}
						}
						si, err := c.DescribeShape(s, f, ln, 0)
						if err != nil {
							return true
						}
						if text != "" && !strings.Contains(strings.ToLower(si.Text), text) {
							return true
						}
						if fill != "" && strings.ToUpper(si.Fill) != fill {
							return true
						}
						if !inRegion(si) {
							return true
						}
						if len(found) >= a.Limit {
							truncated = true
							return false
						}
						found = append(found, foundShape{si, int(idx)})
						return true
					})
					if !cont {
						return nil
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if a.Compact {
		var b strings.Builder
		fmt.Fprintf(&b, "%d shape(s) found%s. Format: page:id type x,y w×h [layer] name / \"text\"\n", len(found),
			map[bool]string{true: " (truncated)", false: ""}[truncated])
		for _, f := range found {
			fmt.Fprintf(&b, "%d:%s", f.Page, compactLine(f.ShapeInfo, ""))
		}
		return mcp.TextResult(b.String()), nil
	}
	return mcp.JSONResult(fmt.Sprintf("%d shape(s) found.", len(found)),
		map[string]any{"count": len(found), "truncated": truncated, "shapes": found}), nil
}

func getPath(ctx context.Context, d *Deps, a *getPathArgs) (*mcp.Result, error) {
	if a.MaxNodes <= 0 {
		a.MaxNodes = 2000
	}
	var b strings.Builder
	segments, subpaths := 0, 0
	truncated := false
	var bb geom.Rect
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "", func(p *pageCtx) error {
			s, err := corel.FindShape(p.page, a.ID)
			if err != nil {
				return err
			}
			if bb, err = corel.BBox(s, p.f); err != nil {
				return err
			}
			var curve *com.Dispatch
			if t, _ := s.GetInt("Type"); t == 3 {
				curve, err = s.GetObj("Curve")
			} else {
				// DisplayCurve gives the curve of any shape without converting it.
				curve, err = s.GetObj("DisplayCurve")
			}
			if err != nil || curve == nil {
				return fmt.Errorf("this shape has no curve geometry (%v)", err)
			}
			num := func(v float64) string { return strconv.FormatFloat(geom.Round(v), 'f', -1, 64) }
			pt := func(x, y float64) string {
				px, py := p.f.ToPage(x, y)
				return num(px) + " " + num(py)
			}
			sps, err := curve.GetObj("SubPaths")
			if err != nil {
				return err
			}
			list, _, err := corel.Collect(sps, 0)
			if err != nil {
				return err
			}
			for _, sp := range list {
				subpaths++
				start, err := sp.GetObj("StartNode")
				if err != nil {
					return err
				}
				sx, _ := start.GetFloat("PositionX")
				sy, _ := start.GetFloat("PositionY")
				b.WriteString("M" + pt(sx, sy))
				segs, err := sp.GetObj("Segments")
				if err != nil {
					return err
				}
				sl, _, err := corel.Collect(segs, 0)
				if err != nil {
					return err
				}
				for _, sg := range sl {
					if segments >= a.MaxNodes {
						truncated = true
						break
					}
					segments++
					end, err := sg.GetObj("EndNode")
					if err != nil {
						return err
					}
					ex, _ := end.GetFloat("PositionX")
					ey, _ := end.GetFloat("PositionY")
					if t, _ := sg.GetInt("Type"); t == 1 {
						c1x, _ := sg.GetFloat("StartingControlPointX")
						c1y, _ := sg.GetFloat("StartingControlPointY")
						c2x, _ := sg.GetFloat("EndingControlPointX")
						c2y, _ := sg.GetFloat("EndingControlPointY")
						b.WriteString(" C" + pt(c1x, c1y) + " " + pt(c2x, c2y) + " " + pt(ex, ey))
					} else {
						b.WriteString(" L" + pt(ex, ey))
					}
				}
				if cl, _ := sp.GetBool("Closed"); cl {
					b.WriteString(" Z")
				}
				if truncated {
					break
				}
				b.WriteString(" ")
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	res := map[string]any{"id": a.ID, "bbox": bb.Rounded(), "subpaths": subpaths, "segments": segments,
		"truncated": truncated, "d": strings.TrimSpace(b.String())}
	return mcp.JSONResult(fmt.Sprintf("Path of shape %d: %d subpath(s), %d segment(s).", a.ID, subpaths, segments), res), nil
}

func selectShapesTool(ctx context.Context, d *Deps, a *selectArgs) (*mcp.Result, error) {
	if len(a.IDs) == 0 {
		return nil, errors.New("ids must not be empty")
	}
	n := 0
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		page, idx, err := c.Page(doc, a.Page)
		if err != nil {
			return err
		}
		if _, active, _ := c.Page(doc, 0); active != idx {
			if _, err := page.Call("Activate"); err != nil {
				return err
			}
		}
		sr, err := c.App.CallObj("CreateShapeRange")
		if err != nil {
			return err
		}
		if a.Add {
			if cur, err := doc.GetObj("SelectionRange"); err == nil {
				if _, err := sr.Call("AddRange", cur); err != nil {
					return err
				}
			}
		}
		for _, id := range a.IDs {
			s, err := corel.FindShape(page, id)
			if err != nil {
				return err
			}
			if _, err := sr.Call("Add", s); err != nil {
				return err
			}
		}
		if _, err := sr.Call("CreateSelection"); err != nil {
			return err
		}
		n64, _ := sr.GetInt("Count")
		n = int(n64)
		_, _ = c.App.Call("Refresh")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(fmt.Sprintf("%d shape(s) selected.", n)), nil
}

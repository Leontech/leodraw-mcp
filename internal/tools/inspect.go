//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type listShapesArgs struct {
	Page      int    `json:"page"`
	Layer     string `json:"layer"`
	Selection bool   `json:"selection"`
	Depth     int    `json:"depth"`
	Limit     int    `json:"limit"`
	Compact   bool   `json:"compact"`
}

type listShapesResult struct {
	Page      int               `json:"page"`
	PageSize  geom.Rect         `json:"page_size_mm"`
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated,omitempty"`
	Shapes    []corel.ShapeInfo `json:"shapes"`
}

func inspectTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "list_shapes",
			Toolset: "inspect",
			Description: "List shapes on a page of the active document (all layers, one layer, or the current selection). " +
				"For each shape returns id, type, name, layer, bounding box (x, y = top-left corner, w, h in mm, page coordinates " +
				"with origin top-left and Y down), fill, outline and text content. Groups report child_count; use depth to list children.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"page":      mcp.Integer("Page number (1-based). Default: the active page."),
				"layer":     mcp.String("Only shapes on this layer (name). Default: all layers of the page."),
				"selection": mcp.Boolean("List only the shapes currently selected in CorelDRAW."),
				"depth":     mcp.Default(mcp.Range(mcp.Integer("How many levels of group children to include (0 = top level only)."), 0, 5), 0),
				"limit":     mcp.Default(mcp.Range(mcp.Integer("Maximum number of top-level shapes to return."), 1, 2000), 200),
				"compact":   mcp.Boolean("One short line per shape (id, type, box, name / text) instead of JSON – for large pages."),
			}),
			Annotations: readOnly("List shapes"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *listShapesArgs) (*mcp.Result, error) {
				return listShapes(ctx, d, a)
			}),
		},
	}
}

func listShapes(ctx context.Context, d *Deps, a *listShapesArgs) (*mcp.Result, error) {
	if a.Limit <= 0 {
		a.Limit = 200
	}
	res := listShapesResult{Shapes: []corel.ShapeInfo{}}
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		return c.WithMM(doc, func() error {
			page, idx, err := c.Page(doc, a.Page)
			if err != nil {
				return err
			}
			res.Page = idx
			f, err := c.Frame(page)
			if err != nil {
				return err
			}
			res.PageSize = geom.Rect{W: geom.Round(f.Width), H: geom.Round(f.Height)}

			type src struct {
				layer  string
				shapes *com.Dispatch
			}
			var sources []src
			switch {
			case a.Selection:
				sel, err := doc.GetObj("SelectionRange")
				if err != nil {
					return err
				}
				sources = append(sources, src{"", sel})
			case a.Layer != "":
				l, err := c.Layer(doc, page, a.Layer)
				if err != nil {
					return err
				}
				sh, err := l.GetObj("Shapes")
				if err != nil {
					return err
				}
				sources = append(sources, src{a.Layer, sh})
			default:
				layers, err := page.GetObj("Layers")
				if err != nil {
					return err
				}
				ls, _, err := corel.Collect(layers, 0)
				if err != nil {
					return err
				}
				for _, l := range ls {
					name, _ := l.GetString("Name")
					sh, err := l.GetObj("Shapes")
					if err != nil {
						return err
					}
					sources = append(sources, src{name, sh})
				}
			}
			for _, s := range sources {
				remaining := a.Limit - len(res.Shapes)
				n, err := s.shapes.GetInt("Count")
				if err != nil {
					return err
				}
				res.Count += int(n)
				if remaining <= 0 {
					res.Truncated = res.Truncated || n > 0
					continue
				}
				items, total, err := corel.Collect(s.shapes, remaining)
				if err != nil {
					return err
				}
				if total > len(items) {
					res.Truncated = true
				}
				for _, it := range items {
					layer := s.layer
					if layer == "" {
						if l, err := it.GetObj("Layer"); err == nil {
							layer, _ = l.GetString("Name")
						}
					}
					si, err := c.DescribeShape(it, f, layer, a.Depth)
					if err != nil {
						return fmt.Errorf("reading shape: %w", err)
					}
					res.Shapes = append(res.Shapes, si)
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("Page %d: %d shape(s)", res.Page, res.Count)
	if res.Truncated {
		summary += fmt.Sprintf(", showing the first %d", len(res.Shapes))
	}
	if a.Compact {
		var b strings.Builder
		b.WriteString(summary + ". Format: id type x,y w×h [layer] name / \"text\"\n")
		var write func(list []corel.ShapeInfo, indent string)
		write = func(list []corel.ShapeInfo, indent string) {
			for _, si := range list {
				b.WriteString(compactLine(si, indent))
				write(si.Children, indent+"  ")
			}
		}
		write(res.Shapes, "")
		return mcp.TextResult(b.String()), nil
	}
	return mcp.JSONResult(summary+".", res), nil
}

// compactLine formats a shape as one short line.
func compactLine(si corel.ShapeInfo, indent string) string {
	line := fmt.Sprintf("%s%d %s %g,%g %g×%g", indent, si.ID, si.Type, si.X, si.Y, si.W, si.H)
	if si.Layer != "" {
		line += " [" + si.Layer + "]"
	}
	if si.Name != "" {
		line += " " + si.Name
	}
	if si.Text != "" {
		t := strings.ReplaceAll(si.Text, "\n", "⏎")
		if r := []rune(t); len(r) > 60 {
			t = string(r[:60]) + "…"
		}
		line += fmt.Sprintf(" %q", t)
	}
	if si.ChildCount > 0 && len(si.Children) == 0 {
		line += fmt.Sprintf(" (%d children)", si.ChildCount)
	}
	return line + "\n"
}

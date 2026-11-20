//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/Leontech/leodraw-mcp/internal/colors"
	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

// styleArgs are common styling options of creation tools.
type styleArgs struct {
	Fill         string   `json:"fill"`
	Outline      string   `json:"outline"`
	OutlineWidth *float64 `json:"outline_width"`
	Name         string   `json:"name"`
	Layer        string   `json:"layer"`
	Page         int      `json:"page"`
}

func styleSchema(props map[string]mcp.Schema) map[string]mcp.Schema {
	props["fill"] = mcp.String(`Fill colour ("#RRGGBB", "rgb(r,g,b)", "cmyk(c,m,y,k)", name or "none"). Default: CorelDRAW default (usually none).`)
	props["outline"] = mcp.String(`Outline colour, or "none" for no outline. Default: CorelDRAW default (usually black hairline).`)
	props["outline_width"] = mcp.Number("Outline width in mm (e.g. 0.2).")
	props["name"] = mcp.String("Object name shown in CorelDRAW's Objects docker.")
	props["layer"] = mcp.String("Layer name to draw on. Default: the active layer.")
	props["page"] = mcp.Integer("Page number (1-based). Default: the active page (another page becomes active).")
	return props
}

type rectArgs struct {
	X            float64   `json:"x"`
	Y            float64   `json:"y"`
	Width        float64   `json:"width"`
	Height       float64   `json:"height"`
	CornerRadius *float64  `json:"corner_radius"`
	Radii        []float64 `json:"radii"`
	styleArgs
}

func drawTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "create_rectangle",
			Toolset: "draw",
			Description: "Draw a rectangle. x, y = top-left corner in mm (page coordinates, origin top-left, Y down). " +
				"Optional rounded corners (one radius or four), fill, outline, name and target layer. Returns the new shape with its id.",
			InputSchema: mcp.Object([]string{"x", "y", "width", "height"}, styleSchema(map[string]mcp.Schema{
				"x":             mcp.Number("Left edge in mm from the page's left edge."),
				"y":             mcp.Number("Top edge in mm from the page's top edge."),
				"width":         mcp.Number("Width in mm (> 0)."),
				"height":        mcp.Number("Height in mm (> 0)."),
				"corner_radius": mcp.Number("Radius of all four corners in mm."),
				"radii":         mcp.Array("Radii of the corners in mm: [top-left, top-right, bottom-right, bottom-left]. Overrides corner_radius.", mcp.Number("radius")),
			})),
			Annotations: modifying("Create rectangle"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *rectArgs) (*mcp.Result, error) {
				return createRectangle(ctx, d, a)
			}),
		},
	}
}

func createRectangle(ctx context.Context, d *Deps, a *rectArgs) (*mcp.Result, error) {
	if a.Width <= 0 || a.Height <= 0 {
		return nil, errors.New("width and height must be greater than 0")
	}
	radii := [4]float64{}
	switch {
	case len(a.Radii) == 4:
		copy(radii[:], a.Radii)
	case len(a.Radii) != 0:
		return nil, errors.New("radii must have exactly 4 values [top-left, top-right, bottom-right, bottom-left]")
	case a.CornerRadius != nil:
		radii = [4]float64{*a.CornerRadius, *a.CornerRadius, *a.CornerRadius, *a.CornerRadius}
	}
	for _, r := range radii {
		if r < 0 || r > math.Min(a.Width, a.Height)/2 {
			return nil, fmt.Errorf("corner radius %g mm is out of range (0 – %g mm)", r, math.Min(a.Width, a.Height)/2)
		}
	}
	style, err := parseStyle(&a.styleArgs)
	if err != nil {
		return nil, err
	}
	var info corel.ShapeInfo
	err = d.S.Run(ctx, func(c *corel.Ctx) error {
		return withTarget(c, &a.styleArgs, "create_rectangle", func(doc, layer *com.Dispatch, f geom.Frame) error {
			xc, yc := f.FromPage(a.X, a.Y)
			s, err := layer.CallObj("CreateRectangle2", xc, yc, a.Width, a.Height,
				radii[0], radii[1], radii[2], radii[3])
			if err != nil {
				return err
			}
			if err := placeTopLeft(s, f, a.X, a.Y); err != nil {
				return err
			}
			if err := applyStyle(c, s, style); err != nil {
				return err
			}
			info, err = c.DescribeShape(s, f, "", 0)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Rectangle created (id %d).", info.ID), info), nil
}

type parsedStyle struct {
	fill, outline *colors.Color
	width         *float64
	name          string
}

func parseStyle(a *styleArgs) (parsedStyle, error) {
	var p parsedStyle
	if a.Fill != "" {
		c, err := colors.Parse(a.Fill)
		if err != nil {
			return p, err
		}
		p.fill = &c
	}
	if a.Outline != "" {
		c, err := colors.Parse(a.Outline)
		if err != nil {
			return p, err
		}
		p.outline = &c
	}
	if a.OutlineWidth != nil {
		if *a.OutlineWidth < 0 || *a.OutlineWidth > 100 {
			return p, errors.New("outline_width must be between 0 and 100 mm")
		}
		p.width = a.OutlineWidth
	}
	p.name = a.Name
	return p, nil
}

func applyStyle(c *corel.Ctx, s *com.Dispatch, p parsedStyle) error {
	if p.fill != nil {
		if err := c.SetFill(s, *p.fill); err != nil {
			return fmt.Errorf("setting fill: %w", err)
		}
	}
	if p.outline != nil || p.width != nil {
		if err := c.SetOutline(s, p.outline, p.width); err != nil {
			return fmt.Errorf("setting outline: %w", err)
		}
	}
	if p.name != "" {
		if err := s.Put("Name", p.name); err != nil {
			return fmt.Errorf("setting name: %w", err)
		}
	}
	return nil
}

// withTarget resolves document, page (activating it if needed) and layer,
// sets mm units and runs fn as one undo step.
func withTarget(c *corel.Ctx, a *styleArgs, opName string, fn func(doc, layer *com.Dispatch, f geom.Frame) error) error {
	doc, err := c.ActiveDocument()
	if err != nil {
		return err
	}
	return c.WithMM(doc, func() error {
		page, idx, err := c.Page(doc, a.Page)
		if err != nil {
			return err
		}
		if _, activeIdx, err := c.Page(doc, 0); err != nil {
			return err
		} else if activeIdx != idx {
			if _, err := page.Call("Activate"); err != nil {
				return err
			}
		}
		layer, err := c.Layer(doc, page, a.Layer)
		if err != nil {
			return err
		}
		f, err := c.Frame(page)
		if err != nil {
			return err
		}
		return c.Write(doc, opName, func() error { return fn(doc, layer, f) })
	})
}

// placeTopLeft moves a shape so that its bounding box's top-left corner is at (x, y) page mm.
func placeTopLeft(s *com.Dispatch, f geom.Frame, x, y float64) error {
	bb, err := corel.BBox(s, f)
	if err != nil {
		return err
	}
	dx, dy := x-bb.X, y-bb.Y
	if math.Abs(dx) < 1e-6 && math.Abs(dy) < 1e-6 {
		return nil
	}
	dxc, dyc := f.DeltaFromPage(dx, dy)
	_, err = s.Call("Move", dxc, dyc)
	return err
}

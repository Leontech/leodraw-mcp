//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/Leontech/leodraw-mcp/internal/colors"
	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

// hairlineMM is CorelDRAW's hairline width (0.003 in).
const hairlineMM = 0.0762

type fillArgs struct {
	targetArgs
	Color    string   `json:"color"`
	Gradient string   `json:"gradient"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Angle    *float64 `json:"angle"`
	MidPoint *int     `json:"midpoint"`
}

type outlineArgs struct {
	targetArgs
	Color          string   `json:"color"`
	Width          *float64 `json:"width"`
	Hairline       bool     `json:"hairline"`
	LineCaps       string   `json:"line_caps"`
	LineJoin       string   `json:"line_join"`
	BehindFill     *bool    `json:"behind_fill"`
	ScaleWithShape *bool    `json:"scale_with_shape"`
}

type shapePropsArgs struct {
	targetArgs
	Name   *string `json:"name"`
	Locked *bool   `json:"locked"`
}

func styleTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "set_fill",
			Toolset: "style",
			Description: "Set the fill of shapes (ids or selection): a uniform colour, \"none\", or a gradient " +
				"(gradient: linear/radial/conical/square from colour \"from\" to colour \"to\"; linear runs in the direction of angle " +
				"(degrees counter-clockwise, 0 = left→right), radial/square from the centre outwards).",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"color":    mcp.String(`Uniform fill colour or "none".`),
				"gradient": mcp.Enum("Gradient (fountain) fill type.", "linear", "radial", "conical", "square"),
				"from":     mcp.String("Gradient start colour."),
				"to":       mcp.String("Gradient end colour."),
				"angle":    mcp.Number("Linear gradient angle in degrees (0 = left to right, 90 = bottom to top)."),
				"midpoint": mcp.Range(mcp.Integer("Gradient midpoint 1–99 (default 50)."), 1, 99),
			})),
			Annotations: modifying("Set fill"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *fillArgs) (*mcp.Result, error) {
				return setFill(ctx, d, a)
			}),
		},
		{
			Name:    "set_outline",
			Toolset: "style",
			Description: "Set the outline of shapes (ids or selection): colour (\"none\" removes it), width in mm or hairline, " +
				"line caps, corner joins, behind fill, scale with shape.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"color":            mcp.String(`Outline colour or "none".`),
				"width":            mcp.Number("Width in mm."),
				"hairline":         mcp.Boolean("Hairline width (0.076 mm – typical for laser / plotter cutting lines)."),
				"line_caps":        mcp.Enum("Line end caps.", "butt", "round", "square"),
				"line_join":        mcp.Enum("Corner joins.", "miter", "round", "bevel"),
				"behind_fill":      mcp.Boolean("Draw the outline behind the fill."),
				"scale_with_shape": mcp.Boolean("Scale the outline width when the shape is resized."),
			})),
			Annotations: modifying("Set outline"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *outlineArgs) (*mcp.Result, error) {
				return setOutline(ctx, d, a)
			}),
		},
		{
			Name:        "set_shape_props",
			Toolset:     "inspect",
			Description: "Set object properties of shapes (ids or selection): name (shown in the Objects docker) and locked.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"name":   mcp.String("New object name (\"\" clears it)."),
				"locked": mcp.Boolean("Lock / unlock the shapes."),
			})),
			Annotations: modifying("Set shape properties"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *shapePropsArgs) (*mcp.Result, error) {
				return setShapeProps(ctx, d, a)
			}),
		},
	}
}

// eachShape runs fn for every target shape inside one undo step and returns their descriptions.
func eachShape(ctx context.Context, d *Deps, t *targetArgs, op string, fn func(c *corel.Ctx, s *com.Dispatch) error) ([]corel.ShapeInfo, error) {
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, t.Page, op, func(p *pageCtx) error {
			_, list, err := p.shapes(t)
			if err != nil {
				return err
			}
			for _, s := range list {
				if err := fn(c, s); err != nil {
					id, _ := s.GetInt("StaticID")
					return fmt.Errorf("shape %d: %w", id, err)
				}
			}
			infos = p.describe(list)
			return nil
		})
	})
	return infos, err
}

func setFill(ctx context.Context, d *Deps, a *fillArgs) (*mcp.Result, error) {
	types := map[string]int{"linear": 1, "radial": 2, "conical": 3, "square": 4}
	var uni, from, to colors.Color
	var err error
	switch {
	case a.Gradient != "":
		if _, ok := types[a.Gradient]; !ok {
			return nil, fmt.Errorf("unknown gradient %q", a.Gradient)
		}
		if a.From == "" || a.To == "" {
			return nil, errors.New("a gradient needs from and to colours")
		}
		if from, err = colors.Parse(a.From); err != nil {
			return nil, err
		}
		if to, err = colors.Parse(a.To); err != nil {
			return nil, err
		}
		if from.Model == colors.None || to.Model == colors.None {
			return nil, errors.New(`gradient colours cannot be "none"`)
		}
	case a.Color != "":
		if uni, err = colors.Parse(a.Color); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("give color, or gradient with from and to")
	}
	mid := 50
	if a.MidPoint != nil {
		mid = *a.MidPoint
	}
	infos, err := eachShape(ctx, d, &a.targetArgs, "set_fill", func(c *corel.Ctx, s *com.Dispatch) error {
		if a.Gradient == "" {
			return c.SetFill(s, uni)
		}
		fill, err := s.GetObj("Fill")
		if err != nil {
			return err
		}
		c1, err := c.NewColor(from)
		if err != nil {
			return err
		}
		c2, err := c.NewColor(to)
		if err != nil {
			return err
		}
		if a.Gradient != "linear" {
			c1, c2 = c2, c1 // CorelDRAW's start colour is the outer edge; LeoDraw's "from" is the centre
		}
		// StartColor, EndColor, Type, Angle, Steps, EdgePad, MidPoint, BlendType, CenterOffsetX, CenterOffsetY
		_, err = fill.Call("ApplyFountainFill", c1, c2, types[a.Gradient], f64(a.Angle, 0), 0, 0, mid, 0, 0.0, 0.0)
		return err
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Fill set on %d shape(s).", len(infos)), infos), nil
}

func setOutline(ctx context.Context, d *Deps, a *outlineArgs) (*mcp.Result, error) {
	caps := map[string]int{"butt": 0, "round": 1, "square": 2}
	joins := map[string]int{"miter": 0, "round": 1, "bevel": 2}
	if _, ok := caps[a.LineCaps]; a.LineCaps != "" && !ok {
		return nil, fmt.Errorf("unknown line_caps %q", a.LineCaps)
	}
	if _, ok := joins[a.LineJoin]; a.LineJoin != "" && !ok {
		return nil, fmt.Errorf("unknown line_join %q", a.LineJoin)
	}
	var col *colors.Color
	if a.Color != "" {
		cc, err := colors.Parse(a.Color)
		if err != nil {
			return nil, err
		}
		col = &cc
	}
	width := a.Width
	if a.Hairline {
		w := hairlineMM
		width = &w
	}
	if width != nil && (*width < 0 || *width > 100) {
		return nil, errors.New("width must be 0 – 100 mm")
	}
	if col == nil && width == nil && a.LineCaps == "" && a.LineJoin == "" && a.BehindFill == nil && a.ScaleWithShape == nil {
		return nil, errors.New("nothing to change – give color, width, hairline, line_caps, line_join, behind_fill or scale_with_shape")
	}
	infos, err := eachShape(ctx, d, &a.targetArgs, "set_outline", func(c *corel.Ctx, s *com.Dispatch) error {
		if col != nil || width != nil {
			if err := c.SetOutline(s, col, width); err != nil {
				return err
			}
		}
		if col != nil && col.Model == colors.None {
			return nil
		}
		ol, err := s.GetObj("Outline")
		if err != nil {
			return err
		}
		if a.LineCaps != "" {
			if err := ol.Put("LineCaps", caps[a.LineCaps]); err != nil {
				return err
			}
		}
		if a.LineJoin != "" {
			if err := ol.Put("LineJoin", joins[a.LineJoin]); err != nil {
				return err
			}
		}
		if a.BehindFill != nil {
			if err := ol.Put("BehindFill", *a.BehindFill); err != nil {
				return err
			}
		}
		if a.ScaleWithShape != nil {
			if err := ol.Put("ScaleWithShape", *a.ScaleWithShape); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Outline set on %d shape(s).", len(infos)), infos), nil
}

func setShapeProps(ctx context.Context, d *Deps, a *shapePropsArgs) (*mcp.Result, error) {
	if a.Name == nil && a.Locked == nil {
		return nil, errors.New("give name and/or locked")
	}
	infos, err := eachShape(ctx, d, &a.targetArgs, "set_shape_props", func(c *corel.Ctx, s *com.Dispatch) error {
		if a.Name != nil {
			if err := s.Put("Name", *a.Name); err != nil {
				return err
			}
		}
		if a.Locked != nil {
			if err := s.Put("Locked", *a.Locked); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Properties set on %d shape(s).", len(infos)), infos), nil
}

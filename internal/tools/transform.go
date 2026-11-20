//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type transformArgs struct {
	targetArgs
	X          *float64 `json:"x"`
	Y          *float64 `json:"y"`
	Anchor     string   `json:"anchor"`
	DX         float64  `json:"dx"`
	DY         float64  `json:"dy"`
	Width      *float64 `json:"width"`
	Height     *float64 `json:"height"`
	KeepAspect *bool    `json:"keep_aspect"`
	Scale      *float64 `json:"scale"`
	Rotate     float64  `json:"rotate"`
	RotateCX   *float64 `json:"rotate_cx"`
	RotateCY   *float64 `json:"rotate_cy"`
	SkewX      float64  `json:"skew_x"`
	SkewY      float64  `json:"skew_y"`
	Flip       string   `json:"flip"`
	Each       bool     `json:"each"`
}

type alignArgs struct {
	targetArgs
	Horizontal string `json:"horizontal"`
	Vertical   string `json:"vertical"`
	To         string `json:"to"`
	ToID       int64  `json:"to_id"`
	AsGroup    bool   `json:"as_group"`
}

type distributeArgs struct {
	targetArgs
	Horizontal string `json:"horizontal"`
	Vertical   string `json:"vertical"`
	Area       string `json:"area"`
}

type duplicateArgs struct {
	targetArgs
	DX    float64 `json:"dx"`
	DY    float64 `json:"dy"`
	Count int     `json:"count"`
}

type orderArgs struct {
	targetArgs
	To    string `json:"to"`
	RefID int64  `json:"ref_id"`
}

type groupArgs struct {
	targetArgs
	Name string `json:"name"`
	Into int64  `json:"into"`
}

type ungroupArgs struct {
	targetArgs
	All bool `json:"all"`
}

func transformTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "transform_shapes",
			Toolset: "transform",
			Description: "Move, resize, scale, rotate, skew and/or flip shapes (ids or selection). Applied in this order: size/scale " +
				"(the anchor point stays in place), rotate, skew, flip, then position (x/y puts the anchor point there) and finally dx/dy. " +
				"By default the shapes are transformed together as one unit; each: true transforms every shape on its own. " +
				"Angles in degrees, positive = counter-clockwise (CorelDRAW convention).",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"x":           mcp.Number("New x of the anchor point (mm). Give only x (or only y) to move along one axis."),
				"y":           mcp.Number("New y of the anchor point (mm)."),
				"anchor":      anchorSchema(),
				"dx":          mcp.Number("Move right by this many mm (negative = left)."),
				"dy":          mcp.Number("Move down by this many mm (negative = up)."),
				"width":       mcp.Number("New width in mm."),
				"height":      mcp.Number("New height in mm."),
				"keep_aspect": mcp.Boolean("When only width or height is given, keep the aspect ratio (default true)."),
				"scale":       mcp.Number("Scale in percent (50 = half size)."),
				"rotate":      mcp.Number("Rotate by this many degrees (counter-clockwise), around the centre unless rotate_cx/cy given."),
				"rotate_cx":   mcp.Number("Rotation centre x (mm)."),
				"rotate_cy":   mcp.Number("Rotation centre y (mm)."),
				"skew_x":      mcp.Number("Horizontal skew in degrees."),
				"skew_y":      mcp.Number("Vertical skew in degrees."),
				"flip":        mcp.Enum("Mirror the shapes.", "horizontal", "vertical", "both"),
				"each":        mcp.Boolean("Transform each shape individually (own centre / anchor)."),
			})),
			Annotations: modifying("Transform shapes"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *transformArgs) (*mcp.Result, error) {
				return transformShapes(ctx, d, a)
			}),
		},
		{
			Name:    "align_shapes",
			Toolset: "transform",
			Description: "Align shapes (ids or selection) horizontally and/or vertically. to: \"selection\" (default – to the bounding box " +
				"of all the given shapes), \"page\" (page edges, or page centre for center), or \"shape\" (to the shape to_id). " +
				"as_group: move all shapes together as one block instead of aligning each.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"horizontal": mcp.Enum("Horizontal alignment.", "left", "center", "right"),
				"vertical":   mcp.Enum("Vertical alignment.", "top", "center", "bottom"),
				"to":         mcp.Enum("Align to what.", "selection", "page", "shape"),
				"to_id":      mcp.Integer("With to = shape: id of the reference shape."),
				"as_group":   mcp.Boolean("Align the shapes as one block."),
			})),
			Annotations: modifying("Align shapes"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *alignArgs) (*mcp.Result, error) {
				return alignShapes(ctx, d, a)
			}),
		},
		{
			Name:    "distribute_shapes",
			Toolset: "transform",
			Description: "Distribute shapes (ids or selection, at least 3 for area = selection) evenly: by their left/center/right " +
				"edges or with equal spacing, horizontally and/or vertically, within their own extent or across the page.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"horizontal": mcp.Enum("Horizontal distribution.", "left", "center", "right", "spacing"),
				"vertical":   mcp.Enum("Vertical distribution.", "top", "center", "bottom", "spacing"),
				"area":       mcp.Enum("Distribute within the shapes' extent (default) or across the page.", "selection", "page"),
			})),
			Annotations: modifying("Distribute shapes"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *distributeArgs) (*mcp.Result, error) {
				return distributeShapes(ctx, d, a)
			}),
		},
		{
			Name:    "duplicate_shapes",
			Toolset: "transform",
			Description: "Duplicate shapes (ids or selection) with an offset in mm (dx right, dy down). count > 1 makes a row of copies, " +
				"each offset from the previous one. Returns the new shapes.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"dx":    mcp.Number("Horizontal offset of each copy in mm."),
				"dy":    mcp.Number("Vertical offset of each copy in mm (positive = down)."),
				"count": mcp.Default(mcp.Range(mcp.Integer("Number of copies."), 1, 500), 1),
			})),
			Annotations: modifying("Duplicate shapes"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *duplicateArgs) (*mcp.Result, error) {
				return duplicateShapes(ctx, d, a)
			}),
		},
		{
			Name:        "order_shapes",
			Toolset:     "transform",
			Description: "Change the stacking order of shapes (ids or selection): to front/back of their layer, one step forward/backward, or in front of / behind the shape ref_id.",
			InputSchema: mcp.Object([]string{"to"}, targetSchema(map[string]mcp.Schema{
				"to":     mcp.Enum("Where to move.", "front", "back", "forward", "backward", "in_front_of", "behind"),
				"ref_id": mcp.Integer("Reference shape for in_front_of / behind."),
			})),
			Annotations: modifying("Order shapes"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *orderArgs) (*mcp.Result, error) {
				return orderShapes(ctx, d, a)
			}),
		},
		{
			Name:        "delete_shapes",
			Toolset:     "transform",
			Description: "Delete shapes (ids or selection). Can be reverted with undo.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{})),
			Annotations: destructive("Delete shapes"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *targetArgs) (*mcp.Result, error) {
				return simpleRangeOp(ctx, d, a, "delete_shapes", "Delete")
			}),
		},
		{
			Name:    "group",
			Toolset: "shaping",
			Description: "Group shapes (ids or selection) into one group, or add them to an existing group (into = group id, " +
				"keeps its name; the group gets a new id). Returns the group.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"name": mcp.String("Name of the group."),
				"into": mcp.Integer("Id of an existing group to add the shapes to."),
			})),
			Annotations: modifying("Group"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *groupArgs) (*mcp.Result, error) {
				return groupShapes(ctx, d, a)
			}),
		},
		{
			Name:        "ungroup",
			Toolset:     "shaping",
			Description: "Ungroup groups (ids or selection) one level, or completely with all: true. Returns the released shapes.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"all": mcp.Boolean("Ungroup nested groups too."),
			})),
			Annotations: modifying("Ungroup"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *ungroupArgs) (*mcp.Result, error) {
				return ungroupShapes(ctx, d, a)
			}),
		},
		{
			Name:        "combine",
			Toolset:     "shaping",
			Description: "Combine shapes (ids or selection) into one curve object (overlaps become holes). Returns the combined curve.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{})),
			Annotations: modifying("Combine"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *targetArgs) (*mcp.Result, error) {
				return combineShapes(ctx, d, a)
			}),
		},
		{
			Name:        "break_apart",
			Toolset:     "shaping",
			Description: "Break combined curves (ids or selection) apart into separate curves, one per subpath. Returns the new shapes.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{})),
			Annotations: modifying("Break apart"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *targetArgs) (*mcp.Result, error) {
				return breakApart(ctx, d, a)
			}),
		},
		{
			Name:        "convert_to_curves",
			Toolset:     "shaping",
			Description: "Convert shapes (ids or selection) – text, rectangles, ellipses, polygons – to editable curves (e.g. text before sending to a laser or plotter).",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{})),
			Annotations: modifying("Convert to curves"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *targetArgs) (*mcp.Result, error) {
				return simpleRangeOp(ctx, d, a, "convert_to_curves", "ConvertToCurves")
			}),
		},
	}
}

func shapesResult(msg string, infos []corel.ShapeInfo) *mcp.Result {
	return mcp.JSONResult(msg, map[string]any{"shapes": infos})
}

func transformShapes(ctx context.Context, d *Deps, a *transformArgs) (*mcp.Result, error) {
	if (a.RotateCX == nil) != (a.RotateCY == nil) {
		return nil, errors.New("give both rotate_cx and rotate_cy")
	}
	if _, err := anchorFrac(a.Anchor); err != nil {
		return nil, err
	}
	if a.Scale != nil && *a.Scale <= 0 {
		return nil, errors.New("scale must be greater than 0")
	}
	flip := map[string]int{"": 0, "horizontal": 1, "vertical": 2, "both": 3}
	fl, ok := flip[a.Flip]
	if !ok {
		return nil, fmt.Errorf("unknown flip %q", a.Flip)
	}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "transform_shapes", func(p *pageCtx) error {
			sr, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			objs := []*com.Dispatch{sr}
			if a.Each {
				objs = list
			}
			for _, o := range objs {
				if a.Scale != nil {
					bb, err := corel.BBox(o, p.f)
					if err != nil {
						return err
					}
					w, h := bb.W**a.Scale/100, bb.H**a.Scale/100
					if err := resize(o, p.f, &w, &h, false, a.Anchor); err != nil {
						return err
					}
				}
				if a.Width != nil || a.Height != nil {
					if err := resize(o, p.f, a.Width, a.Height, a.KeepAspect == nil || *a.KeepAspect, a.Anchor); err != nil {
						return err
					}
				}
				if a.Rotate != 0 {
					var cx, cy float64
					if a.RotateCX != nil {
						cx, cy = p.f.FromPage(*a.RotateCX, *a.RotateCY)
					} else if cx, cy, err = center(o); err != nil {
						return err
					}
					if _, err := o.Call("RotateEx", a.Rotate, cx, cy); err != nil {
						return err
					}
				}
				if a.SkewX != 0 || a.SkewY != 0 {
					cx, cy, err := center(o)
					if err != nil {
						return err
					}
					if _, err := o.Call("SkewEx", a.SkewX, a.SkewY, cx, cy); err != nil {
						return err
					}
				}
				if fl != 0 {
					if _, err := o.Call("Flip", fl); err != nil {
						return err
					}
				}
				if err := placeAtOpt(o, p.f, a.X, a.Y, a.Anchor); err != nil {
					return err
				}
				if err := moveBy(o, p.f, a.DX, a.DY); err != nil {
					return err
				}
			}
			infos = p.describe(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Transformed %d shape(s).", len(infos)), infos), nil
}

func alignShapes(ctx context.Context, d *Deps, a *alignArgs) (*mcp.Result, error) {
	h := map[string]int{"": 0, "left": 1, "right": 2, "center": 3}
	v := map[string]int{"": 0, "top": 4, "bottom": 8, "center": 12}
	hv, ok1 := h[a.Horizontal]
	vv, ok2 := v[a.Vertical]
	if !ok1 || !ok2 {
		return nil, errors.New("horizontal must be left/center/right and vertical top/center/bottom")
	}
	typ := hv | vv
	if typ == 0 {
		return nil, errors.New("give horizontal and/or vertical")
	}
	const textBBox = 2 // cdrTextAlignBoundingBox
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "align_shapes", func(p *pageCtx) error {
			sr, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			switch a.To {
			case "", "selection":
				if a.AsGroup {
					return errors.New("as_group needs to = page or shape")
				}
				// AlignToShapeRange(sr) with the range itself does nothing, so
				// align each shape to the matching edge / centre of the range.
				var px, py float64
				left, _ := sr.GetFloat("LeftX")
				w, _ := sr.GetFloat("SizeWidth")
				top, _ := sr.GetFloat("TopY")
				bottom, _ := sr.GetFloat("BottomY")
				cx, cy, err2 := center(sr)
				if err2 != nil {
					return err2
				}
				switch a.Horizontal {
				case "left":
					px = left
				case "right":
					px = left + w
				default:
					px = cx
				}
				switch a.Vertical {
				case "top":
					py = top
				case "bottom":
					py = bottom
				default:
					py = cy
				}
				_, err = sr.Call("AlignToPoint", typ, px, py, textBBox)
			case "page":
				if a.AsGroup {
					_, err = sr.Call("AlignRangeToPage", typ)
				} else {
					_, err = sr.Call("AlignToPage", typ, textBBox)
				}
			case "shape":
				if a.ToID == 0 {
					return errors.New("to = shape needs to_id")
				}
				ref, err2 := corel.FindShape(p.page, a.ToID)
				if err2 != nil {
					return err2
				}
				if a.AsGroup {
					_, err = sr.Call("AlignRangeToShape", typ, ref)
				} else {
					_, err = sr.Call("AlignToShape", typ, ref, textBBox)
				}
			default:
				return fmt.Errorf("unknown to %q", a.To)
			}
			if err != nil {
				return err
			}
			infos = p.describe(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Aligned %d shape(s).", len(infos)), infos), nil
}

func distributeShapes(ctx context.Context, d *Deps, a *distributeArgs) (*mcp.Result, error) {
	h := map[string]int{"": 0, "right": 4, "left": 5, "center": 6, "spacing": 7}
	v := map[string]int{"": 0, "top": 4, "bottom": 5, "center": 6, "spacing": 7}
	area := map[string]int{"": 0, "selection": 0, "page": 1}
	hv, ok1 := h[a.Horizontal]
	vv, ok2 := v[a.Vertical]
	ar, ok3 := area[a.Area]
	if !ok1 || !ok2 || !ok3 {
		return nil, errors.New("invalid horizontal, vertical or area value")
	}
	if hv == 0 && vv == 0 {
		return nil, errors.New("give horizontal and/or vertical")
	}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "distribute_shapes", func(p *pageCtx) error {
			sr, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			if len(list) < 2 || (ar == 0 && len(list) < 3) {
				return errors.New("distributing needs at least 3 shapes (2 with area = page)")
			}
			if _, err := sr.Call("AlignAndDistribute", hv, vv, 0, ar, false, 2, 0.0, 0.0, com.Nothing); err != nil {
				return err
			}
			infos = p.describe(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Distributed %d shape(s).", len(infos)), infos), nil
}

func duplicateShapes(ctx context.Context, d *Deps, a *duplicateArgs) (*mcp.Result, error) {
	if a.Count == 0 {
		a.Count = 1
	}
	if a.Count < 1 || a.Count > 500 {
		return nil, errors.New("count must be 1 – 500")
	}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "duplicate_shapes", func(p *pageCtx) error {
			sr, _, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			ox, oy := p.f.DeltaFromPage(a.DX, a.DY)
			cur := sr
			var all []*com.Dispatch
			for i := 0; i < a.Count; i++ {
				dup, err := cur.CallObj("Duplicate", ox, oy)
				if err != nil {
					return err
				}
				all = append(all, rangeList(dup)...)
				cur = dup
			}
			infos = p.describe(all)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Created %d copies.", len(infos)), infos), nil
}

func orderShapes(ctx context.Context, d *Deps, a *orderArgs) (*mcp.Result, error) {
	methods := map[string]string{"front": "OrderToFront", "back": "OrderToBack", "forward": "OrderForwardOne",
		"backward": "OrderBackOne", "in_front_of": "OrderFrontOf", "behind": "OrderBackOf"}
	m, ok := methods[a.To]
	if !ok {
		return nil, fmt.Errorf("unknown to %q", a.To)
	}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "order_shapes", func(p *pageCtx) error {
			sr, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			if a.To == "in_front_of" || a.To == "behind" {
				if a.RefID == 0 {
					return errors.New("in_front_of / behind needs ref_id")
				}
				ref, err := corel.FindShape(p.page, a.RefID)
				if err != nil {
					return err
				}
				_, err = sr.Call(m, ref)
				if err != nil {
					return err
				}
			} else if _, err := sr.Call(m); err != nil {
				return err
			}
			infos = p.describe(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Reordered %d shape(s).", len(infos)), infos), nil
}

// simpleRangeOp calls a parameterless ShapeRange method (Delete, ConvertToCurves).
func simpleRangeOp(ctx context.Context, d *Deps, a *targetArgs, op, method string) (*mcp.Result, error) {
	n := 0
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, op, func(p *pageCtx) error {
			sr, list, err := p.shapes(a)
			if err != nil {
				return err
			}
			n = len(list)
			if _, err := sr.Call(method); err != nil {
				return err
			}
			if method != "Delete" {
				infos = p.describe(list)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if method == "Delete" {
		return mcp.TextResult(fmt.Sprintf("Deleted %d shape(s).", n)), nil
	}
	return shapesResult(fmt.Sprintf("%s done on %d shape(s).", op, n), infos), nil
}

func groupShapes(ctx context.Context, d *Deps, a *groupArgs) (*mcp.Result, error) {
	var info corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "group", func(p *pageCtx) error {
			sr, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			name := a.Name
			if a.Into != 0 {
				grp, err := corel.FindShape(p.page, a.Into)
				if err != nil {
					return err
				}
				if t, _ := grp.GetInt("Type"); t != 7 {
					return fmt.Errorf("shape %d is not a group", a.Into)
				}
				if name == "" {
					name, _ = grp.GetString("Name")
				}
				children, err := grp.CallObj("UngroupEx")
				if err != nil {
					return err
				}
				all := rangeList(children)
				for _, s := range list {
					if id, _ := s.GetInt("StaticID"); id != a.Into {
						all = append(all, s)
					}
				}
				list = all
				if sr, err = newRange(c, all); err != nil {
					return err
				}
			}
			if len(list) < 2 {
				return errors.New("grouping needs at least 2 shapes")
			}
			g, err := sr.CallObj("Group")
			if err != nil {
				return err
			}
			if n, _ := g.GetObj("Shapes"); n == nil {
				return errors.New("CorelDRAW did not create the group")
			} else if cnt, _ := n.GetInt("Count"); cnt == 0 {
				return errors.New("CorelDRAW did not create the group")
			}
			if name != "" {
				if err := g.Put("Name", name); err != nil {
					return err
				}
			}
			info, err = c.DescribeShape(g, p.f, "", 0)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Group created (id %d, %d shapes).", info.ID, info.ChildCount), info), nil
}

func ungroupShapes(ctx context.Context, d *Deps, a *ungroupArgs) (*mcp.Result, error) {
	method := "UngroupEx"
	if a.All {
		method = "UngroupAllEx"
	}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "ungroup", func(p *pageCtx) error {
			sr, _, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			res, err := sr.CallObj(method)
			if err != nil {
				return err
			}
			infos = p.describe(rangeList(res))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Ungrouped into %d shape(s).", len(infos)), infos), nil
}

func combineShapes(ctx context.Context, d *Deps, a *targetArgs) (*mcp.Result, error) {
	var info corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "combine", func(p *pageCtx) error {
			sr, list, err := p.shapes(a)
			if err != nil {
				return err
			}
			if len(list) < 2 {
				return errors.New("combining needs at least 2 shapes")
			}
			s, err := sr.CallObj("Combine")
			if err != nil {
				return err
			}
			info, err = c.DescribeShape(s, p.f, "", 0)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Combined into curve id %d.", info.ID), info), nil
}

func breakApart(ctx context.Context, d *Deps, a *targetArgs) (*mcp.Result, error) {
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "break_apart", func(p *pageCtx) error {
			_, list, err := p.shapes(a)
			if err != nil {
				return err
			}
			var all []*com.Dispatch
			for _, s := range list {
				res, err := s.CallObj("BreakApartEx")
				if err != nil {
					return err
				}
				all = append(all, rangeList(res)...)
			}
			infos = p.describe(all)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return shapesResult(fmt.Sprintf("Broken apart into %d shape(s).", len(infos)), infos), nil
}

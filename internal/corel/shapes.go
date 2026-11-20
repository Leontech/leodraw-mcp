//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package corel

import (
	"fmt"
	"math"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/colors"
	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/geom"
)

// CorelDRAW constants (VGCore type library).
const (
	cdrMillimeter = 3

	cdrCurrentPage = 1
	cdrSelection   = 2

	cdrPNG = 802

	cdrGroupShape = 7
	cdrTextShape  = 6
)

var shapeTypes = map[int64]string{
	0: "none", 1: "rectangle", 2: "ellipse", 3: "curve", 4: "polygon", 5: "bitmap",
	6: "text", 7: "group", 8: "selection", 9: "guideline", 10: "blend_group",
	11: "extrude_group", 12: "ole_object", 13: "contour_group", 14: "linear_dimension",
	15: "bevel_group", 16: "drop_shadow_group", 17: "3d_object", 18: "artistic_media_group",
	19: "connector", 20: "mesh_fill", 21: "custom", 22: "custom_effect_group",
	23: "symbol", 24: "html_form_object", 25: "html_active_object", 26: "perfect_shape",
	27: "eps",
}

// ShapeTypeName returns a readable name for a cdrShapeType value.
func ShapeTypeName(t int64) string {
	if s, ok := shapeTypes[t]; ok {
		return s
	}
	return fmt.Sprintf("type_%d", t)
}

var fillTypes = map[int64]string{
	0: "none", 2: "fountain", 3: "postscript", 8: "texture", 9: "pattern", 10: "hatch",
}

// Frame reads the page geometry (document unit must be mm).
func (c *Ctx) Frame(page *com.Dispatch) (geom.Frame, error) {
	var v [5]float64
	for i, name := range []string{"LeftX", "TopY", "BottomY", "SizeWidth", "SizeHeight"} {
		f, err := page.GetFloat(name)
		if err != nil {
			return geom.Frame{}, err
		}
		v[i] = f
	}
	return geom.NewFrame(v[0], v[1], v[2], v[3], v[4]), nil
}

// BBox returns the bounding box of a shape or shape range in page coordinates.
func BBox(obj *com.Dispatch, f geom.Frame) (geom.Rect, error) {
	var v [4]float64
	for i, name := range []string{"LeftX", "TopY", "SizeWidth", "SizeHeight"} {
		x, err := obj.GetFloat(name)
		if err != nil {
			return geom.Rect{}, err
		}
		v[i] = x
	}
	x, y := f.ToPage(v[0], v[1])
	return geom.Rect{X: x, Y: y, W: v[2], H: v[3]}, nil
}

// ShapeInfo is the JSON description of a shape.
type ShapeInfo struct {
	ID         int64       `json:"id"`
	Type       string      `json:"type"`
	Name       string      `json:"name,omitempty"`
	Layer      string      `json:"layer,omitempty"`
	X          float64     `json:"x"`
	Y          float64     `json:"y"`
	W          float64     `json:"w"`
	H          float64     `json:"h"`
	Fill       string      `json:"fill,omitempty"`
	Outline    string      `json:"outline,omitempty"`
	Text       string      `json:"text,omitempty"`
	ChildCount int         `json:"child_count,omitempty"`
	Children   []ShapeInfo `json:"children,omitempty"`
}

// DescribeShape builds ShapeInfo; depth > 0 includes group children.
func (c *Ctx) DescribeShape(s *com.Dispatch, f geom.Frame, layer string, depth int) (ShapeInfo, error) {
	var si ShapeInfo
	var err error
	if si.ID, err = s.GetInt("StaticID"); err != nil {
		return si, err
	}
	t, err := s.GetInt("Type")
	if err != nil {
		return si, err
	}
	si.Type = ShapeTypeName(t)
	si.Name, _ = s.GetString("Name")
	si.Layer = layer
	if bb, err := BBox(s, f); err == nil {
		bb = bb.Rounded()
		si.X, si.Y, si.W, si.H = bb.X, bb.Y, bb.W, bb.H
	}
	si.Fill = c.fillText(s)
	si.Outline = c.outlineText(s)
	if t == cdrTextShape {
		if story, err := s.GetObj("Text"); err == nil {
			if st, err := story.GetObj("Story"); err == nil {
				txt, _ := st.GetString("Text")
				txt = strings.ReplaceAll(txt, "\r", "\n")
				if r := []rune(txt); len(r) > 300 {
					txt = string(r[:300]) + "…"
				}
				si.Text = txt
			}
		}
	}
	if t == cdrGroupShape {
		if shapes, err := s.GetObj("Shapes"); err == nil {
			if depth <= 0 {
				n, _ := shapes.GetInt("Count")
				si.ChildCount = int(n)
			} else if items, total, err := Collect(shapes, 500); err == nil {
				si.ChildCount = total
				for _, it := range items {
					if child, err := c.DescribeShape(it, f, "", depth-1); err == nil {
						si.Children = append(si.Children, child)
					}
				}
			}
		}
	}
	return si, nil
}

func (c *Ctx) fillText(s *com.Dispatch) string {
	fill, err := s.GetObj("Fill")
	if err != nil {
		return ""
	}
	t, err := fill.GetInt("Type")
	if err != nil {
		return ""
	}
	if t == 1 { // uniform
		if col, err := fill.GetObj("UniformColor"); err == nil {
			return c.ColorText(col)
		}
		return "uniform"
	}
	if name, ok := fillTypes[t]; ok {
		return name
	}
	return fmt.Sprintf("fill_type_%d", t)
}

func (c *Ctx) outlineText(s *com.Dispatch) string {
	ol, err := s.GetObj("Outline")
	if err != nil {
		return ""
	}
	if t, err := ol.GetInt("Type"); err == nil && t == 0 {
		return "none"
	}
	w, err := ol.GetFloat("Width")
	if err != nil {
		return ""
	}
	if w <= 0 {
		return "none"
	}
	col := "?"
	if co, err := ol.GetObj("Color"); err == nil {
		col = c.ColorText(co)
	}
	return fmt.Sprintf("%s %smm", col, trimFloat(w, 3))
}

// ColorText returns a colour as "#RRGGBB" (converted to RGB on a copy).
func (c *Ctx) ColorText(col *com.Dispatch) string {
	if cp, err := c.App.CallObj("CreateColor"); err == nil {
		if _, err := cp.Call("CopyAssign", col); err == nil {
			if _, err := cp.Call("ConvertToRGB"); err == nil {
				r, e1 := cp.GetInt("RGBRed")
				g, e2 := cp.GetInt("RGBGreen")
				b, e3 := cp.GetInt("RGBBlue")
				if e1 == nil && e2 == nil && e3 == nil {
					return fmt.Sprintf("#%02X%02X%02X", r, g, b)
				}
			}
		}
	}
	if name, err := col.GetString("Name"); err == nil && name != "" {
		return name
	}
	return "?"
}

// NewColor creates a CorelDRAW Color object.
func (c *Ctx) NewColor(col colors.Color) (*com.Dispatch, error) {
	switch col.Model {
	case colors.RGB:
		return c.App.CallObj("CreateRGBColor", col.R, col.G, col.B)
	case colors.CMYK:
		return c.App.CallObj("CreateCMYKColor", col.C, col.M, col.Y, col.K)
	}
	return nil, fmt.Errorf("color %s cannot be created", col)
}

// SetFill applies a uniform fill or removes the fill.
func (c *Ctx) SetFill(s *com.Dispatch, col colors.Color) error {
	fill, err := s.GetObj("Fill")
	if err != nil {
		return err
	}
	if col.Model == colors.None {
		_, err = fill.Call("ApplyNoFill")
		return err
	}
	cc, err := c.NewColor(col)
	if err != nil {
		return err
	}
	_, err = fill.Call("ApplyUniformFill", cc)
	return err
}

// SetOutline sets outline colour and/or width (mm). A "none" colour removes the outline.
func (c *Ctx) SetOutline(s *com.Dispatch, col *colors.Color, width *float64) error {
	ol, err := s.GetObj("Outline")
	if err != nil {
		return err
	}
	if col != nil && col.Model == colors.None {
		_, err = ol.Call("SetNoOutline")
		return err
	}
	// colour first: on a shape without an outline, assigning the colour turns
	// the outline on with the default width, which would override a width set before
	if col != nil {
		cc, err := c.NewColor(*col)
		if err != nil {
			return err
		}
		cur, err := ol.GetObj("Color")
		if err != nil {
			return err
		}
		if _, err := cur.Call("CopyAssign", cc); err != nil {
			return err
		}
	}
	if width != nil {
		if err := ol.Put("Width", *width); err != nil {
			return err
		}
	}
	return nil
}

// FindShape finds a shape by StaticID on page (all layers, recursively into groups).
// Page.FindShape is not used: with late binding all parameters must be passed and
// an empty Name then means "a shape with an empty name", not "any name".
func FindShape(page *com.Dispatch, id int64) (*com.Dispatch, error) {
	layers, err := page.GetObj("Layers")
	if err != nil {
		return nil, err
	}
	ls, _, err := Collect(layers, 0)
	if err != nil {
		return nil, err
	}
	for _, l := range ls {
		shapes, err := l.GetObj("Shapes")
		if err != nil {
			continue
		}
		if s := findIn(shapes, id, 0); s != nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("shape id %d not found on this page (use list_shapes to get valid ids)", id)
}

func findIn(shapes *com.Dispatch, id int64, depth int) *com.Dispatch {
	items, _, err := Collect(shapes, 0)
	if err != nil {
		return nil
	}
	for _, it := range items {
		if sid, err := it.GetInt("StaticID"); err == nil && sid == id {
			return it
		}
		if depth < 20 {
			if t, err := it.GetInt("Type"); err == nil && t == cdrGroupShape {
				if sub, err := it.GetObj("Shapes"); err == nil {
					if s := findIn(sub, id, depth+1); s != nil {
						return s
					}
				}
			}
		}
	}
	return nil
}

func trimFloat(v float64, prec int) string {
	p := math.Pow(10, float64(prec))
	v = math.Round(v*p) / p
	return fmt.Sprintf("%g", v)
}

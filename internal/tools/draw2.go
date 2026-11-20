//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type ellipseArgs struct {
	X          float64  `json:"x"`
	Y          float64  `json:"y"`
	Width      float64  `json:"width"`
	Height     float64  `json:"height"`
	StartAngle *float64 `json:"start_angle"`
	EndAngle   *float64 `json:"end_angle"`
	Pie        bool     `json:"pie"`
	styleArgs
}

type polygonArgs struct {
	CX          float64  `json:"cx"`
	CY          float64  `json:"cy"`
	Radius      float64  `json:"radius"`
	Sides       int      `json:"sides"`
	Star        bool     `json:"star"`
	InnerRadius *float64 `json:"inner_radius"`
	Rotation    float64  `json:"rotation"`
	styleArgs
}

type lineArgs struct {
	Points [][]float64 `json:"points"`
	Closed bool        `json:"closed"`
	styleArgs
}

type pathArgs struct {
	D       string   `json:"d"`
	Scale   *float64 `json:"scale"`
	OffsetX float64  `json:"offset_x"`
	OffsetY float64  `json:"offset_y"`
	X       *float64 `json:"x"`
	Y       *float64 `json:"y"`
	Anchor  string   `json:"anchor"`
	styleArgs
}

type importArgs struct {
	Path       string   `json:"path"`
	URL        string   `json:"url"`
	Data       string   `json:"data_base64"`
	Ext        string   `json:"extension"`
	SVG        string   `json:"svg"`
	X          *float64 `json:"x"`
	Y          *float64 `json:"y"`
	Anchor     string   `json:"anchor"`
	Width      *float64 `json:"width"`
	Height     *float64 `json:"height"`
	KeepAspect *bool    `json:"keep_aspect"`
	Ungroup    bool     `json:"ungroup"`
	Name       string   `json:"name"`
	Layer      string   `json:"layer"`
	Page       int      `json:"page"`
}

func draw2Tools(d *Deps) []*mcp.Tool {
	importProps := func(p map[string]mcp.Schema) map[string]mcp.Schema {
		p["x"] = mcp.Number("Place the imported objects so that their anchor point is at x (mm). Default: where CorelDRAW puts them.")
		p["y"] = mcp.Number("Place the imported objects so that their anchor point is at y (mm).")
		p["anchor"] = anchorSchema()
		p["width"] = mcp.Number("Resize the imported objects to this width (mm).")
		p["height"] = mcp.Number("Resize the imported objects to this height (mm).")
		p["keep_aspect"] = mcp.Boolean("Keep the aspect ratio when only width or height is given (default true).")
		p["ungroup"] = mcp.Boolean("Ungroup the imported group one level.")
		p["name"] = mcp.String("Name for the imported object.")
		p["layer"] = mcp.String("Layer name. Default: the active layer.")
		p["page"] = mcp.Integer("Page number (1-based). Default: the active page.")
		return p
	}
	return []*mcp.Tool{
		{
			Name:    "create_ellipse",
			Toolset: "draw",
			Description: "Draw an ellipse or circle given by its bounding box (x, y = top-left corner, width, height in mm). " +
				"With start_angle/end_angle it becomes an arc (open) or a pie slice (pie: true). Angles in degrees, counter-clockwise from 3 o'clock.",
			InputSchema: mcp.Object([]string{"x", "y", "width", "height"}, styleSchema(map[string]mcp.Schema{
				"x":           mcp.Number("Left edge of the bounding box in mm."),
				"y":           mcp.Number("Top edge of the bounding box in mm."),
				"width":       mcp.Number("Width in mm (> 0). Equal width and height = circle."),
				"height":      mcp.Number("Height in mm (> 0)."),
				"start_angle": mcp.Number("Arc start angle in degrees (counter-clockwise from 3 o'clock)."),
				"end_angle":   mcp.Number("Arc end angle in degrees."),
				"pie":         mcp.Boolean("With angles: draw a closed pie slice instead of an open arc."),
			})),
			Annotations: modifying("Create ellipse"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *ellipseArgs) (*mcp.Result, error) {
				return createEllipse(ctx, d, a)
			}),
		},
		{
			Name:    "create_polygon",
			Toolset: "draw",
			Description: "Draw a regular polygon or star centred at (cx, cy) mm with the given outer radius. " +
				"rotation in degrees (counter-clockwise); with rotation 0 one vertex points straight up.",
			InputSchema: mcp.Object([]string{"cx", "cy", "radius", "sides"}, styleSchema(map[string]mcp.Schema{
				"cx":           mcp.Number("Centre x in mm."),
				"cy":           mcp.Number("Centre y in mm."),
				"radius":       mcp.Number("Outer radius (centre to vertex) in mm."),
				"sides":        mcp.Range(mcp.Integer("Number of sides / star points."), 3, 500),
				"star":         mcp.Boolean("Draw a star."),
				"inner_radius": mcp.Number("Star: inner radius in mm (default radius / 2)."),
				"rotation":     mcp.Number("Rotation in degrees, counter-clockwise."),
			})),
			Annotations: modifying("Create polygon"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *polygonArgs) (*mcp.Result, error) {
				return createPolygon(ctx, d, a)
			}),
		},
		{
			Name:    "create_line",
			Toolset: "draw",
			Description: "Draw a line, polyline or closed polygon through points [[x, y], …] in mm (page coordinates, Y down). " +
				"Two points = a straight line. closed: true joins the last point to the first.",
			InputSchema: mcp.Object([]string{"points"}, styleSchema(map[string]mcp.Schema{
				"points": mcp.Array("Points [[x, y], …] in mm, at least 2.", mcp.Array("point [x, y]", mcp.Number("mm"))),
				"closed": mcp.Boolean("Close the path."),
			})),
			Annotations: modifying("Create line"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *lineArgs) (*mcp.Result, error) {
				return createLine(ctx, d, a)
			}),
		},
		{
			Name:    "create_path",
			Toolset: "draw",
			Description: "Draw a native CorelDRAW curve from SVG path data (d attribute: M L H V C S Q T A Z, absolute and relative). " +
				"Path coordinates are mm on the page (origin top-left, Y down) multiplied by scale and shifted by offset_x/offset_y. " +
				"Optionally place the result with x/y/anchor. Several subpaths (holes) are kept in one curve.",
			InputSchema: mcp.Object([]string{"d"}, styleSchema(map[string]mcp.Schema{
				"d":        mcp.String(`SVG path data, e.g. "M10 10 L50 10 L30 40 Z".`),
				"scale":    mcp.Number("Multiply path coordinates by this factor (default 1; e.g. 0.2646 for px at 96 DPI → mm)."),
				"offset_x": mcp.Number("Add to all x coordinates after scaling (mm)."),
				"offset_y": mcp.Number("Add to all y coordinates after scaling (mm)."),
				"x":        mcp.Number("Move the finished curve so that its anchor point is at x (mm)."),
				"y":        mcp.Number("Move the finished curve so that its anchor point is at y (mm)."),
				"anchor":   anchorSchema(),
			})),
			Annotations: modifying("Create path"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *pathArgs) (*mcp.Result, error) {
				return createPath(ctx, d, a)
			}),
		},
		{
			Name:    "import_file",
			Toolset: "draw",
			Description: "Import a file (SVG, DXF, DWG, PDF, AI, EPS, CDR, PNG, JPG, TIFF, …) into the document – from a path on this " +
				"Windows PC, from an http(s) URL (downloaded first), or from base64 data. Optionally place and resize the imported objects.",
			InputSchema: mcp.Object(nil, importProps(map[string]mcp.Schema{
				"path":        mcp.String(`Absolute file path, e.g. "C:\\Users\\me\\logo.svg".`),
				"url":         mcp.String("http(s) URL of the file to download and import."),
				"data_base64": mcp.String("File content as base64 (alternative to path / url); give extension too."),
				"extension":   mcp.String(`File type for data_base64 or a URL without extension, e.g. "png", "svg", "pdf".`),
			})),
			Annotations: modifying("Import file"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *importArgs) (*mcp.Result, error) {
				if a.SVG != "" {
					return nil, errors.New("import_file takes path, url or data_base64; use import_svg for SVG markup")
				}
				cleanup, err := fetchImport(ctx, a)
				if err != nil {
					return nil, err
				}
				defer cleanup()
				return importFile(ctx, d, a)
			}),
		},
		{
			Name:    "import_svg",
			Toolset: "draw",
			Description: "Import SVG markup (a complete <svg> document as text) into the document, e.g. an icon or logo you wrote. " +
				"Give the svg element width/height in mm (width=\"50mm\") for exact size, or use width/height here. Optionally place it.",
			InputSchema: mcp.Object([]string{"svg"}, importProps(map[string]mcp.Schema{
				"svg": mcp.String(`SVG document text starting with <svg …>.`),
			})),
			Annotations: modifying("Import SVG"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *importArgs) (*mcp.Result, error) {
				if a.Path != "" {
					return nil, errors.New("import_svg takes svg text; use import_file for files")
				}
				return importSVG(ctx, d, a)
			}),
		},
	}
}

// createAndStyle is shared by simple creation tools.
func createAndStyle(ctx context.Context, d *Deps, st *styleArgs, op, label string, make func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error)) (*mcp.Result, error) {
	style, err := parseStyle(st)
	if err != nil {
		return nil, err
	}
	var info corel.ShapeInfo
	err = d.S.Run(ctx, func(c *corel.Ctx) error {
		return withTarget(c, st, op, func(doc, layer *com.Dispatch, f geom.Frame) error {
			s, err := make(c, doc, layer, f)
			if err != nil {
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
	return mcp.JSONResult(fmt.Sprintf("%s created (id %d).", label, info.ID), info), nil
}

func createEllipse(ctx context.Context, d *Deps, a *ellipseArgs) (*mcp.Result, error) {
	if a.Width <= 0 || a.Height <= 0 {
		return nil, errors.New("width and height must be greater than 0")
	}
	arc := a.StartAngle != nil || a.EndAngle != nil
	start, end := 90.0, 90.0 // equal angles = full ellipse
	if arc {
		start, end = f64(a.StartAngle, 0), f64(a.EndAngle, 360)
		if math.Mod(math.Abs(end-start), 360) < 1e-9 {
			arc = false
			start, end = 90, 90
		}
	}
	label := "Ellipse"
	if arc {
		label = "Arc"
		if a.Pie {
			label = "Pie"
		}
	}
	return createAndStyle(ctx, d, &a.styleArgs, "create_ellipse", label, func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error) {
		cx, cy := f.FromPage(a.X+a.Width/2, a.Y+a.Height/2)
		// CorelDRAW's ellipse angles run clockwise on the page; LeoDraw's are
		// counter-clockwise, so the arc from start to end is mirrored.
		// Negative angles crash CorelDRAW 2026, so they are normalised to 0–360.
		cs, ce := start, end
		if arc {
			cs, ce = norm360(-end), norm360(-start)
		}
		s, err := layer.CallObj("CreateEllipse2", cx, cy, a.Width/2, a.Height/2, cs, ce, a.Pie)
		if err != nil {
			return nil, err
		}
		if !arc {
			if err := placeTopLeft(s, f, a.X, a.Y); err != nil {
				return nil, err
			}
		}
		return s, nil
	})
}

func createPolygon(ctx context.Context, d *Deps, a *polygonArgs) (*mcp.Result, error) {
	if a.Radius <= 0 {
		return nil, errors.New("radius must be greater than 0")
	}
	if a.Sides < 3 || a.Sides > 500 {
		return nil, errors.New("sides must be 3 – 500")
	}
	inner := f64(a.InnerRadius, a.Radius/2)
	if a.Star && (inner <= 0 || inner >= a.Radius) {
		return nil, errors.New("inner_radius must be between 0 and radius")
	}
	label := "Polygon"
	if a.Star {
		label = "Star"
	}
	return createAndStyle(ctx, d, &a.styleArgs, "create_polygon", label, func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error) {
		// Built as a curve from computed vertices: exact geometry independent of
		// CorelDRAW's polygon tool settings (sharpness, inner radius rules).
		n := a.Sides
		var pts [][2]float64
		for i := 0; i < n; i++ {
			ang := (90 + a.Rotation + 360*float64(i)/float64(n)) * math.Pi / 180
			pts = append(pts, [2]float64{a.CX + a.Radius*math.Cos(ang), a.CY - a.Radius*math.Sin(ang)})
			if a.Star {
				ang2 := ang + math.Pi/float64(n)
				pts = append(pts, [2]float64{a.CX + inner*math.Cos(ang2), a.CY - inner*math.Sin(ang2)})
			}
		}
		return polyline(doc, layer, f, pts, true)
	})
}

func createLine(ctx context.Context, d *Deps, a *lineArgs) (*mcp.Result, error) {
	if len(a.Points) < 2 {
		return nil, errors.New("points needs at least 2 points [[x, y], …]")
	}
	pts := make([][2]float64, len(a.Points))
	for i, p := range a.Points {
		if len(p) != 2 {
			return nil, fmt.Errorf("point %d must be [x, y]", i)
		}
		pts[i] = [2]float64{p[0], p[1]}
	}
	label := "Line"
	if a.Closed {
		label = "Closed polyline"
	} else if len(pts) > 2 {
		label = "Polyline"
	}
	return createAndStyle(ctx, d, &a.styleArgs, "create_line", label, func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error) {
		return polyline(doc, layer, f, pts, a.Closed)
	})
}

// polyline creates a curve shape through page points.
func polyline(doc, layer *com.Dispatch, f geom.Frame, pts [][2]float64, closed bool) (*com.Dispatch, error) {
	curve, err := doc.CallObj("CreateCurve")
	if err != nil {
		return nil, err
	}
	x, y := f.FromPage(pts[0][0], pts[0][1])
	sp, err := curve.CallObj("CreateSubPath", x, y)
	if err != nil {
		return nil, err
	}
	for _, p := range pts[1:] {
		x, y := f.FromPage(p[0], p[1])
		if _, err := sp.Call("AppendLineSegment", x, y, false); err != nil {
			return nil, err
		}
	}
	if closed {
		if err := sp.Put("Closed", true); err != nil {
			return nil, err
		}
	}
	return layer.CallObj("CreateCurve", curve)
}

func createPath(ctx context.Context, d *Deps, a *pathArgs) (*mcp.Result, error) {
	subs, err := geom.ParseSVGPath(a.D)
	if err != nil {
		return nil, fmt.Errorf("invalid path data: %w", err)
	}
	scale := f64(a.Scale, 1)
	if scale <= 0 {
		return nil, errors.New("scale must be greater than 0")
	}
	return createAndStyle(ctx, d, &a.styleArgs, "create_path", "Curve", func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error) {
		tr := func(p geom.Point) (float64, float64) {
			return f.FromPage(p.X*scale+a.OffsetX, p.Y*scale+a.OffsetY)
		}
		curve, err := doc.CallObj("CreateCurve")
		if err != nil {
			return nil, err
		}
		for _, sp := range subs {
			if len(sp.Segments) == 0 {
				continue
			}
			x, y := tr(sp.Start)
			csp, err := curve.CallObj("CreateSubPath", x, y)
			if err != nil {
				return nil, err
			}
			for i, seg := range sp.Segments {
				// The closing segment back to the start is added by Closed.
				if sp.Closed && i == len(sp.Segments)-1 && !seg.Cubic &&
					math.Abs(seg.To.X-sp.Start.X) < 1e-9 && math.Abs(seg.To.Y-sp.Start.Y) < 1e-9 {
					break
				}
				tx, ty := tr(seg.To)
				if seg.Cubic {
					c1x, c1y := tr(seg.C1)
					c2x, c2y := tr(seg.C2)
					_, err = csp.Call("AppendCurveSegment2", tx, ty, c1x, c1y, c2x, c2y, false)
				} else {
					_, err = csp.Call("AppendLineSegment", tx, ty, false)
				}
				if err != nil {
					return nil, err
				}
			}
			if sp.Closed {
				if err := csp.Put("Closed", true); err != nil {
					return nil, err
				}
			}
		}
		s, err := layer.CallObj("CreateCurve", curve)
		if err != nil {
			return nil, err
		}
		if err := placeAtOpt(s, f, a.X, a.Y, a.Anchor); err != nil {
			return nil, err
		}
		return s, nil
	})
}

func importSVG(ctx context.Context, d *Deps, a *importArgs) (*mcp.Result, error) {
	if !strings.Contains(a.SVG, "<svg") {
		return nil, errors.New(`svg must be an SVG document containing an <svg …> element`)
	}
	dir := filepath.Join(os.TempDir(), "LeoDraw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, fmt.Sprintf("import-%d.svg", time.Now().UnixNano()))
	if err := os.WriteFile(file, []byte(a.SVG), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(file)
	a.Path = file
	return importFile(ctx, d, a)
}

func importFile(ctx context.Context, d *Deps, a *importArgs) (*mcp.Result, error) {
	if !filepath.IsAbs(a.Path) {
		return nil, fmt.Errorf("path must be absolute, got %q", a.Path)
	}
	if !fileExists(a.Path) {
		return nil, fmt.Errorf("file not found: %s", a.Path)
	}
	if _, err := anchorFrac(a.Anchor); err != nil {
		return nil, err
	}
	st := &styleArgs{Layer: a.Layer, Page: a.Page}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withTarget(c, st, "import", func(doc, layer *com.Dispatch, f geom.Frame) error {
			if _, err := doc.Call("ClearSelection"); err != nil {
				return err
			}
			opts, err := c.App.CallObj("CreateStructImportOptions")
			if err != nil {
				return err
			}
			res, err := layer.Method("ImportEx", a.Path, 0, opts)
			if err != nil {
				return fmt.Errorf("import failed: %w", err)
			}
			if flt, ok := res.(*com.Dispatch); ok && flt != nil {
				if _, err := flt.Method("Finish"); err != nil {
					return fmt.Errorf("import failed: %w", err)
				}
			}
			sr, err := doc.GetObj("SelectionRange")
			if err != nil {
				return err
			}
			if n, _ := sr.GetInt("Count"); n == 0 {
				return errors.New("CorelDRAW imported nothing (unsupported or empty file?)")
			}
			if a.Ungroup {
				if ug, err := sr.CallObj("UngroupEx"); err == nil {
					sr = ug
				}
			}
			if a.Width != nil || a.Height != nil {
				if err := resize(sr, f, a.Width, a.Height, a.KeepAspect == nil || *a.KeepAspect, "top-left"); err != nil {
					return err
				}
			}
			if err := placeAtOpt(sr, f, a.X, a.Y, a.Anchor); err != nil {
				return err
			}
			list := rangeList(sr)
			if a.Name != "" && len(list) == 1 {
				_ = list[0].Put("Name", a.Name)
			}
			for _, s := range list {
				if si, err := c.DescribeShape(s, f, "", 0); err == nil {
					infos = append(infos, si)
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Imported %d object(s).", len(infos)), map[string]any{"shapes": infos}), nil
}

// resize sets the size of obj keeping the given anchor point in place.
// Only one of w/h with keepAspect scales proportionally.
func resize(obj *com.Dispatch, f geom.Frame, w, h *float64, keepAspect bool, anchor string) error {
	bb, err := corel.BBox(obj, f)
	if err != nil {
		return err
	}
	if bb.W <= 0 && bb.H <= 0 {
		return errors.New("object has no size")
	}
	nw, nh := bb.W, bb.H
	switch {
	case w != nil && h != nil:
		nw, nh = *w, *h
	case w != nil:
		nw = *w
		if keepAspect && bb.W > 0 {
			nh = bb.H * *w / bb.W
		}
	case h != nil:
		nh = *h
		if keepAspect && bb.H > 0 {
			nw = bb.W * *h / bb.H
		}
	default:
		return nil
	}
	if nw < 0 || nh < 0 {
		return errors.New("width and height must not be negative")
	}
	px, py, err := anchorPoint(obj, f, anchor)
	if err != nil {
		return err
	}
	if _, err := obj.Call("SetSize", nw, nh); err != nil {
		return err
	}
	return placeAt(obj, f, px, py, anchor)
}

// norm360 maps an angle to [0, 360).
func norm360(a float64) float64 {
	a = math.Mod(a, 360)
	if a < 0 {
		a += 360
	}
	return a
}

// fetchImport turns url / data_base64 into a temporary file in a.Path.
func fetchImport(ctx context.Context, a *importArgs) (func(), error) {
	n := 0
	for _, v := range []string{a.Path, a.URL, a.Data} {
		if v != "" {
			n++
		}
	}
	if n != 1 {
		return nil, errors.New("give exactly one of path, url or data_base64")
	}
	if a.Path != "" {
		return func() {}, nil
	}
	ext := strings.TrimPrefix(strings.ToLower(a.Ext), ".")
	var data []byte
	if a.URL != "" {
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("url must be http(s): %q", a.URL)
		}
		if ext == "" {
			ext = strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("download failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download failed: HTTP %s", resp.Status)
		}
		const limit = 200 << 20
		if data, err = io.ReadAll(io.LimitReader(resp.Body, limit+1)); err != nil {
			return nil, fmt.Errorf("download failed: %w", err)
		}
		if len(data) > limit {
			return nil, errors.New("file is larger than 200 MB")
		}
		if ext == "" {
			ext = extFromContentType(resp.Header.Get("Content-Type"))
		}
	} else {
		s := a.Data
		if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:") && i > 0 {
			if ext == "" {
				ext = extFromContentType(s[5:i])
			}
			s = s[i+8:]
		}
		var err error
		if data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(s)); err != nil {
			return nil, fmt.Errorf("data_base64 is not valid base64: %w", err)
		}
	}
	if ext == "" || strings.ContainsAny(ext, `/\:`) {
		return nil, errors.New("cannot tell the file type – give extension, e.g. \"png\"")
	}
	// a private folder per import, so the file (and the imported object) keeps a readable name
	dir := filepath.Join(os.TempDir(), "LeoDraw", fmt.Sprintf("import-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	base := "import"
	if a.URL != "" {
		if u, err := url.Parse(a.URL); err == nil {
			b := strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
			if b != "" && b != "." && b != "/" && !strings.ContainsAny(b, `\:*?"<>|`) {
				base = b
			}
		}
	}
	file := filepath.Join(dir, base+"."+ext)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	a.Path, a.URL, a.Data = file, "", ""
	return func() { os.RemoveAll(dir) }, nil
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch ct {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/svg+xml":
		return "svg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/tiff":
		return "tif"
	case "image/bmp":
		return "bmp"
	case "application/pdf":
		return "pdf"
	}
	return ""
}

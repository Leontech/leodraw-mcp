//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type previewArgs struct {
	Page          int      `json:"page"`
	IDs           []int64  `json:"ids"`
	Selection     bool     `json:"selection"`
	MaxPx         int      `json:"max_px"`
	Transparent   bool     `json:"transparent"`
	X             *float64 `json:"x"`
	Y             *float64 `json:"y"`
	W             *float64 `json:"w"`
	H             *float64 `json:"h"`
	IncludeHidden bool     `json:"include_hidden"`
}

type previewInfo struct {
	Page    int       `json:"page"`
	AreaMM  geom.Rect `json:"area_mm"`
	ImagePx [2]int    `json:"image_px"`
	MMPerPx float64   `json:"mm_per_px"`
}

func previewTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "render_preview",
			Toolset: "preview",
			Description: "Render a PNG preview so you can SEE the drawing: the whole page (default), a region of it (x, y, w, h in mm " +
				"– zoom into details), specific shapes (ids) or the current selection. Use it after every change to verify the result. " +
				"Hidden layers are left out unless include_hidden. The caption gives the rendered area in page mm and the scale " +
				"(mm per pixel) so you can map pixels back to coordinates.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"page":           mcp.Integer("Page number (1-based). Default: the active page."),
				"ids":            mcp.Array("Render only these shapes (ids from list_shapes).", mcp.Integer("shape id")),
				"selection":      mcp.Boolean("Render only the shapes currently selected in CorelDRAW."),
				"max_px":         mcp.Default(mcp.Range(mcp.Integer("Size of the longer image side in pixels."), 64, 4096), 1024),
				"transparent":    mcp.Boolean("Transparent background instead of white."),
				"x":              mcp.Number("Region left edge (mm) – render only the region x, y, w, h of the page."),
				"y":              mcp.Number("Region top edge (mm)."),
				"w":              mcp.Number("Region width (mm)."),
				"h":              mcp.Number("Region height (mm)."),
				"include_hidden": mcp.Boolean("Also render shapes on hidden layers."),
			}),
			Annotations: readOnly("Render preview"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *previewArgs) (*mcp.Result, error) {
				return renderPreview(ctx, d, a)
			}),
		},
	}
}

func renderPreview(ctx context.Context, d *Deps, a *previewArgs) (*mcp.Result, error) {
	if a.MaxPx <= 0 {
		a.MaxPx = 1024
	}
	a.MaxPx = max(64, min(4096, a.MaxPx))
	region := a.X != nil || a.Y != nil || a.W != nil || a.H != nil
	if region {
		if a.X == nil || a.Y == nil || a.W == nil || a.H == nil {
			return nil, errors.New("a region needs all of x, y, w, h")
		}
		if *a.W <= 0 || *a.H <= 0 {
			return nil, errors.New("region w and h must be greater than 0")
		}
		if len(a.IDs) > 0 || a.Selection {
			return nil, errors.New("a region cannot be combined with ids or selection")
		}
	}
	var info previewInfo
	file := filepath.Join(os.TempDir(), fmt.Sprintf("leodraw-preview-%d.png", time.Now().UnixNano()))
	defer os.Remove(file)

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
			info.Page = idx
			// Export works on the active page: switch temporarily if needed.
			active, activeIdx, err := c.Page(doc, 0)
			if err != nil {
				return err
			}
			if activeIdx != idx {
				if _, err := page.Call("Activate"); err != nil {
					return err
				}
				defer active.Call("Activate")
			}
			f, err := c.Frame(page)
			if err != nil {
				return err
			}
			rangeMode := cdrCurrentPage
			area := geom.Rect{W: f.Width, H: f.Height}
			if len(a.IDs) > 0 || a.Selection {
				rangeMode = cdrSelection
				if len(a.IDs) > 0 {
					restore, err := selectShapes(c, doc, page, a.IDs)
					if err != nil {
						return err
					}
					defer restore()
				}
				sel, err := doc.GetObj("SelectionRange")
				if err != nil {
					return err
				}
				if n, _ := sel.GetInt("Count"); n == 0 {
					return errors.New("nothing is selected in CorelDRAW")
				}
				if area, err = corel.BBox(sel, f); err != nil {
					return err
				}
			}
			var exportArea *com.Dispatch // whole page, not just the extent of its objects
			if rangeMode == cdrCurrentPage {
				if region {
					area = geom.Rect{X: *a.X, Y: *a.Y, W: *a.W, H: *a.H}
					xc, y1 := f.FromPage(area.X, area.Y)
					_, y2 := f.FromPage(area.X, area.Y+area.H)
					if exportArea, err = c.App.CallObj("CreateRect", xc, math.Min(y1, y2), area.W, area.H); err != nil {
						return err
					}
				} else {
					exportArea, _ = page.GetObj("BoundingBox")
				}
				if !a.IncludeHidden {
					restore, used, err := selectVisible(c, doc, page)
					if err != nil {
						return err
					}
					if used {
						defer restore()
						rangeMode = cdrSelection
					}
				}
			}
			if area.W <= 0 || area.H <= 0 {
				return errors.New("the area to render is empty")
			}
			scale := float64(a.MaxPx) / math.Max(area.W, area.H) // px per mm
			wpx := max(1, int(math.Round(area.W*scale)))
			hpx := max(1, int(math.Round(area.H*scale)))
			dpi := max(1, int(math.Round(scale*25.4)))
			if err := exportPNG(c, doc, file, rangeMode, wpx, hpx, dpi, a.Transparent, exportArea); err != nil {
				return err
			}
			info.AreaMM = area.Rounded()
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	png, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("CorelDRAW did not produce the preview image: %w", err)
	}
	w, h, ok := pngSize(png)
	if !ok {
		return nil, errors.New("CorelDRAW produced an invalid PNG")
	}
	info.ImagePx = [2]int{w, h}
	if w > 0 {
		info.MMPerPx = math.Round(info.AreaMM.W/float64(w)*10000) / 10000
	}
	caption := fmt.Sprintf("Page %d, area x=%g y=%g w=%g h=%g mm, image %dx%d px, %g mm/px.",
		info.Page, info.AreaMM.X, info.AreaMM.Y, info.AreaMM.W, info.AreaMM.H, w, h, info.MMPerPx)
	return mcp.ImageResult(png, "image/png", caption), nil
}

// exportPNG exports the current page or selection to a PNG file. It tries
// several ways (the export API differs between CorelDRAW versions) and reports
// all errors when none works.
func exportPNG(c *corel.Ctx, doc *com.Dispatch, file string, rangeMode, wpx, hpx, dpi int, transparent bool, area *com.Dispatch) error {
	opts, optErr := c.App.CallObj("CreateStructExportOptions")
	if optErr == nil {
		for _, p := range []struct {
			name string
			v    any
		}{
			{"ImageType", cdrRGBColorImage},
			{"AntiAliasingType", cdrNormalAntiAliasing},
			{"ResolutionX", dpi},
			{"ResolutionY", dpi},
			{"SizeX", wpx},
			{"SizeY", hpx},
			{"MaintainAspect", true},
			{"Overwrite", true},
			{"Transparent", transparent},
		} {
			_ = opts.Put(p.name, p.v) // optional settings: older versions may lack some
		}
		if area != nil {
			if err := opts.Put("ExportArea", area); err != nil {
				c.Logf("preview: cannot set ExportArea: %v", err)
			}
		}
	}
	finish := func(v any, err error) error {
		if err != nil {
			return err
		}
		if f, ok := v.(*com.Dispatch); ok && f != nil {
			if _, err := f.Method("Finish"); err != nil {
				return err
			}
		}
		return nil
	}
	// Late-bound CorelDRAW calls need ALL parameters, optional ones included
	// (an omitted typed optional parameter gives "type mismatch"); optional
	// object parameters are passed as Nothing.
	type variant struct {
		name string
		run  func() error
	}
	var variants []variant
	if opts != nil {
		variants = append(variants,
			variant{"Export(file, PNG, range, options, Nothing)", func() error {
				_, err := doc.Method("Export", file, cdrPNG, rangeMode, opts, com.Nothing)
				return err
			}},
			variant{"ExportEx(file, PNG, range, options, Nothing)", func() error {
				return finish(doc.Method("ExportEx", file, cdrPNG, rangeMode, opts, com.Nothing))
			}},
		)
	}
	variants = append(variants,
		variant{"ExportBitmap(all 16 parameters)", func() error {
			return finish(doc.Method("ExportBitmap", file, cdrPNG, rangeMode, cdrRGBColorImage, wpx, hpx, dpi, dpi,
				cdrNormalAntiAliasing, false, transparent, true, false, 0, com.Nothing, areaOrNothing(area)))
		}},
	)
	var errs []string
	if optErr != nil {
		errs = append(errs, "CreateStructExportOptions: "+optErr.Error())
	}
	for _, v := range variants {
		_ = os.Remove(file)
		err := v.run()
		if err == nil {
			if _, statErr := os.Stat(file); statErr == nil {
				c.Logf("preview export OK via %s", v.name)
				return nil
			}
			err = fmt.Errorf("no file was written")
		}
		errs = append(errs, v.name+": "+err.Error())
	}
	return fmt.Errorf("export failed – tried:\n- %s", strings.Join(errs, "\n- "))
}

// selectShapes selects the given shapes and returns a function restoring the previous selection.
func selectShapes(c *corel.Ctx, doc, page *com.Dispatch, ids []int64) (func(), error) {
	prev, err := doc.GetObj("SelectionRange")
	if err != nil {
		return nil, err
	}
	sr, err := c.App.CallObj("CreateShapeRange")
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		s, err := corel.FindShape(page, id)
		if err != nil {
			return nil, err
		}
		if _, err := sr.Call("Add", s); err != nil {
			return nil, err
		}
	}
	if _, err := sr.Call("CreateSelection"); err != nil {
		return nil, err
	}
	return func() {
		if n, _ := prev.GetInt("Count"); n > 0 {
			_, _ = prev.Call("CreateSelection")
		} else {
			_, _ = doc.Call("ClearSelection")
		}
	}, nil
}

func pngSize(b []byte) (w, h int, ok bool) {
	if len(b) < 24 || string(b[1:4]) != "PNG" || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24])), true
}

// CorelDRAW constants used here.
const (
	cdrCurrentPage = 1
	cdrSelection   = 2
	cdrPNG         = 802

	cdrRGBColorImage      = 4
	cdrNormalAntiAliasing = 1
)

func areaOrNothing(a *com.Dispatch) *com.Dispatch {
	if a == nil {
		return com.Nothing
	}
	return a
}

// selectVisible selects all shapes on the visible layers of page when some
// layer is hidden, so that a selection export leaves the hidden ones out.
// used is false (and nothing is changed) when no layer with shapes is hidden.
func selectVisible(c *corel.Ctx, doc, page *com.Dispatch) (restore func(), used bool, err error) {
	layers, err := page.GetObj("Layers")
	if err != nil {
		return nil, false, err
	}
	ls, _, err := corel.Collect(layers, 0)
	if err != nil {
		return nil, false, err
	}
	var visible []*com.Dispatch
	hidden := false
	for _, l := range ls {
		if sp, _ := l.GetBool("IsSpecialLayer"); sp {
			continue
		}
		sh, err := l.GetObj("Shapes")
		if err != nil {
			continue
		}
		n, _ := sh.GetInt("Count")
		if v, _ := l.GetBool("Visible"); !v {
			hidden = hidden || n > 0
			continue
		}
		items, _, _ := corel.Collect(sh, 0)
		visible = append(visible, items...)
	}
	if !hidden {
		return nil, false, nil
	}
	if len(visible) == 0 {
		return nil, false, errors.New("all shapes on this page are on hidden layers (use include_hidden)")
	}
	prev, err := doc.GetObj("SelectionRange")
	if err != nil {
		return nil, false, err
	}
	sr, err := newRange(c, visible)
	if err != nil {
		return nil, false, err
	}
	if _, err := sr.Call("CreateSelection"); err != nil {
		return nil, false, err
	}
	return func() {
		if n, _ := prev.GetInt("Count"); n > 0 {
			_, _ = prev.Call("CreateSelection")
		} else {
			_, _ = doc.Call("ClearSelection")
		}
	}, true, nil
}

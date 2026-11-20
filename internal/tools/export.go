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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

// exportFormats maps a format name to the cdrFilter value; bitmap formats are marked.
var exportFormats = map[string]struct {
	filter int
	bitmap bool
}{
	"svg": {1345, false}, "svgz": {1347, false}, "dxf": {1296, false}, "dwg": {1328, false},
	"eps": {1289, false}, "ai": {1305, false}, "plt": {1281, false}, "emf": {1300, false},
	"wmf": {1294, false}, "cmx": {1793, false},
	"png": {802, true}, "jpg": {774, true}, "tif": {772, true}, "bmp": {769, true},
	"gif": {773, true}, "webp": {825, true}, "psd": {788, true},
	"pdf": {1333, false},
}

var formatAliases = map[string]string{"jpeg": "jpg", "tiff": "tif", "hpgl": "plt"}

func formatNames() []string {
	var n []string
	for k := range exportFormats {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

type exportArgs struct {
	Path          string  `json:"path"`
	Format        string  `json:"format"`
	Range         string  `json:"range"`
	Page          int     `json:"page"`
	IDs           []int64 `json:"ids"`
	PDFPages      string  `json:"pdf_pages"`
	DPI           int     `json:"dpi"`
	WidthPx       int     `json:"width_px"`
	HeightPx      int     `json:"height_px"`
	Transparent   bool    `json:"transparent"`
	Overwrite     bool    `json:"overwrite"`
	IncludeHidden bool    `json:"include_hidden"`
}

func exportTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "export",
			Toolset: "export",
			Description: "Export to a file: PDF, SVG, DXF, DWG, EPS, AI, PLT (HPGL), EMF, PNG, JPG, TIFF, WebP … (format from the file extension " +
				"or `format`). range: \"page\" (default, the active page or `page`), \"all\" (all pages – PDF), \"selection\" or \"shapes\" (ids). " +
				"Bitmaps: dpi (default 300) or width_px/height_px, transparent background for PNG. Existing files are kept unless overwrite: true.",
			InputSchema: mcp.Object([]string{"path"}, map[string]mcp.Schema{
				"path":           mcp.String(`Absolute output path, e.g. "C:\\Users\\me\\Desktop\\sign.pdf".`),
				"format":         mcp.Enum("File format. Default: from the extension.", formatNames()...),
				"range":          mcp.Enum("What to export.", "page", "all", "selection", "shapes"),
				"page":           mcp.Integer("Page number for range = page / shapes. Default: the active page."),
				"ids":            mcp.Array("Shape ids for range = shapes.", mcp.Integer("id")),
				"pdf_pages":      mcp.String(`PDF only: page range like "1-3,5" (overrides range).`),
				"dpi":            mcp.Range(mcp.Integer("Bitmap resolution (default 300)."), 10, 2400),
				"width_px":       mcp.Integer("Bitmap width in pixels (overrides dpi)."),
				"height_px":      mcp.Integer("Bitmap height in pixels."),
				"transparent":    mcp.Boolean("Transparent background (PNG, TIFF, WebP)."),
				"overwrite":      mcp.Boolean("Replace an existing file."),
				"include_hidden": mcp.Boolean("range = page: also export shapes on hidden layers (default: left out; PDF follows CorelDRAW's own rules)."),
			}),
			Annotations: destructive("Export"),
			Handler: handler(5*defaultTimeout, func(ctx context.Context, a *exportArgs) (*mcp.Result, error) {
				return exportFile(ctx, d, a)
			}),
		},
	}
}

func exportFile(ctx context.Context, d *Deps, a *exportArgs) (*mcp.Result, error) {
	if !filepath.IsAbs(a.Path) {
		return nil, fmt.Errorf("path must be absolute, got %q", a.Path)
	}
	format := strings.ToLower(a.Format)
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(a.Path)), ".")
	}
	if f, ok := formatAliases[format]; ok {
		format = f
	}
	ef, ok := exportFormats[format]
	if !ok {
		return nil, fmt.Errorf("unknown format %q (supported: %s)", format, strings.Join(formatNames(), ", "))
	}
	rng := a.Range
	if rng == "" {
		rng = "page"
		if len(a.IDs) > 0 {
			rng = "shapes"
		}
	}
	switch rng {
	case "page", "all", "selection":
	case "shapes":
		if len(a.IDs) == 0 {
			return nil, errors.New("range = shapes needs ids")
		}
	default:
		return nil, fmt.Errorf("unknown range %q", rng)
	}
	if rng == "all" && format != "pdf" && ef.bitmap {
		return nil, errors.New("range = all is supported for PDF and vector formats; export bitmaps page by page")
	}
	if fileExists(a.Path) && !a.Overwrite {
		return nil, fmt.Errorf("%s already exists – set overwrite: true to replace it", a.Path)
	}
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
		return nil, err
	}
	dpi := a.DPI
	if dpi == 0 {
		dpi = 300
	}
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
			if _, active, _ := c.Page(doc, 0); active != idx && rng != "all" && rng != "selection" {
				if _, err := page.Call("Activate"); err != nil {
					return err
				}
			}
			if rng == "shapes" {
				restore, err := selectShapes(c, doc, page, a.IDs)
				if err != nil {
					return err
				}
				defer restore()
			}
			if rng == "selection" {
				if sr, err := doc.GetObj("SelectionRange"); err == nil {
					if n, _ := sr.GetInt("Count"); n == 0 {
						return errors.New("nothing is selected in CorelDRAW")
					}
				}
			}
			_ = os.Remove(a.Path) // overwrite confirmed above
			if format == "pdf" {
				return exportPDF(doc, a.Path, rng, a.PDFPages)
			}
			cdrRange := map[string]int{"all": 0, "page": 1, "selection": 2, "shapes": 2}[rng]
			opts, err := c.App.CallObj("CreateStructExportOptions")
			if err != nil {
				return err
			}
			_ = opts.Put("Overwrite", true)
			var area *com.Dispatch
			if rng == "page" {
				area, _ = page.GetObj("BoundingBox")
				if !a.IncludeHidden {
					restore, used, err := selectVisible(c, doc, page)
					if err != nil {
						return err
					}
					if used {
						defer restore()
						cdrRange = cdrSelection
					}
				}
			}
			if ef.bitmap {
				var wmm, hmm float64
				if area != nil {
					wmm, _ = area.GetFloat("Width")
					hmm, _ = area.GetFloat("Height")
				} else if sr, err := doc.GetObj("SelectionRange"); err == nil {
					wmm, _ = sr.GetFloat("SizeWidth")
					hmm, _ = sr.GetFloat("SizeHeight")
				}
				wpx, hpx := a.WidthPx, a.HeightPx
				switch {
				case wpx > 0 && hpx == 0 && wmm > 0:
					hpx = int(math.Round(float64(wpx) * hmm / wmm))
				case hpx > 0 && wpx == 0 && hmm > 0:
					wpx = int(math.Round(float64(hpx) * wmm / hmm))
				case wpx == 0 && hpx == 0:
					wpx = int(math.Round(wmm / 25.4 * float64(dpi)))
					hpx = int(math.Round(hmm / 25.4 * float64(dpi)))
				}
				if wpx < 1 || hpx < 1 || wpx > 30000 || hpx > 30000 {
					return fmt.Errorf("bitmap size %d × %d px is out of range", wpx, hpx)
				}
				imageType := cdrRGBColorImage
				for _, p := range []struct {
					n string
					v any
				}{{"ImageType", imageType}, {"AntiAliasingType", cdrNormalAntiAliasing}, {"ResolutionX", dpi}, {"ResolutionY", dpi},
					{"SizeX", wpx}, {"SizeY", hpx}, {"MaintainAspect", true}, {"Transparent", a.Transparent}} {
					_ = opts.Put(p.n, p.v)
				}
			}
			if area != nil {
				if err := opts.Put("ExportArea", area); err != nil {
					c.Logf("export: cannot set ExportArea: %v", err)
				}
			}
			if _, err := doc.Method("Export", a.Path, ef.filter, cdrRange, opts, com.Nothing); err != nil {
				c.Logf("export: Export failed (%v), trying ExportEx", err)
				res, err2 := doc.Method("ExportEx", a.Path, ef.filter, cdrRange, opts, com.Nothing)
				if err2 != nil {
					return fmt.Errorf("export failed: %v; ExportEx: %v", err, err2)
				}
				if f, ok := res.(*com.Dispatch); ok && f != nil {
					if _, err := f.Method("Finish"); err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(a.Path)
	if err != nil {
		return nil, fmt.Errorf("CorelDRAW reported success but no file was written to %s", a.Path)
	}
	return mcp.JSONResult(fmt.Sprintf("Exported %s (%s, %d bytes).", a.Path, strings.ToUpper(format), st.Size()),
		map[string]any{"path": a.Path, "format": format, "range": rng, "bytes": st.Size()}), nil
}

func exportPDF(doc *com.Dispatch, path, rng, pages string) error {
	set, err := doc.GetObj("PDFSettings")
	if err != nil {
		return err
	}
	pr := map[string]int{"all": 0, "page": 1, "selection": 2, "shapes": 2}[rng]
	if pages != "" {
		pr = 3
		if err := set.Put("PageRange", pages); err != nil {
			return err
		}
	}
	if err := set.Put("PublishRange", pr); err != nil {
		return err
	}
	_, err = doc.Method("PublishToPDF", path)
	return err
}

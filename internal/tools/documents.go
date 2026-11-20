//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

const cdrCDR = 1795

type docRef struct {
	Document int    `json:"document"`
	Name     string `json:"name"`
}

func docRefSchema(p map[string]mcp.Schema) map[string]mcp.Schema {
	p["document"] = mcp.Integer("Document number from list_documents (1-based). Default: the active document.")
	p["name"] = mcp.String("Document name (alternative to document).")
	return p
}

type documentInfo struct {
	Index  int    `json:"document,omitempty"`
	Name   string `json:"name"`
	File   string `json:"file,omitempty"`
	Pages  int64  `json:"pages"`
	Active bool   `json:"active,omitempty"`
	Dirty  bool   `json:"unsaved_changes,omitempty"`
}

type newDocArgs struct {
	Width     *float64 `json:"width"`
	Height    *float64 `json:"height"`
	Pages     int      `json:"pages"`
	Path      string   `json:"path"`
	Overwrite bool     `json:"overwrite"`
}

type openDocArgs struct {
	Path string `json:"path"`
}

type saveAsArgs struct {
	docRef
	Path      string `json:"path"`
	Version   int    `json:"version"`
	Overwrite bool   `json:"overwrite"`
}

type closeDocArgs struct {
	docRef
	Save    bool `json:"save"`
	Discard bool `json:"discard"`
}

func documentTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        "list_documents",
			Toolset:     "app",
			Description: "List the documents open in CorelDRAW: number, name, file, page count, active, unsaved changes.",
			InputSchema: mcp.Object(nil, nil),
			Annotations: readOnly("List documents"),
			Handler: handler(defaultTimeout, func(ctx context.Context, _ *struct{}) (*mcp.Result, error) {
				return listDocuments(ctx, d)
			}),
		},
		{
			Name:        "activate_document",
			Toolset:     "app",
			Description: "Make another open document the active one (all other tools work on the active document).",
			InputSchema: mcp.Object(nil, docRefSchema(map[string]mcp.Schema{})),
			Annotations: modifying("Activate document"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *docRef) (*mcp.Result, error) {
				return docOp(ctx, d, a, func(c *corel.Ctx, doc *com.Dispatch) (string, error) {
					_, err := doc.Call("Activate")
					n, _ := doc.GetString("Name")
					return fmt.Sprintf("Document %q is active.", n), err
				})
			}),
		},
		{
			Name:    "new_document",
			Toolset: "app",
			Description: "Create a new document (default page A4 portrait 210 × 297 mm) and make it active. " +
				"Give path (absolute .cdr) to save it right away – that is how the document gets its name " +
				"(CorelDRAW 2026 freezes when a document is renamed through automation).",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"width":     mcp.Number("Page width in mm."),
				"height":    mcp.Number("Page height in mm."),
				"pages":     mcp.Default(mcp.Range(mcp.Integer("Number of pages."), 1, 999), 1),
				"path":      mcp.String(`Save the new document to this absolute .cdr path (gives it its name), e.g. "D:\\Work\\label.cdr".`),
				"overwrite": mcp.Boolean("Replace an existing file at path."),
			}),
			Annotations: modifying("New document"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *newDocArgs) (*mcp.Result, error) {
				return newDocument(ctx, d, a)
			}),
		},
		{
			Name:        "open_document",
			Toolset:     "app",
			Description: "Open a file (CDR, AI, PDF, SVG, …) as a new document and make it active. path is absolute on this PC.",
			InputSchema: mcp.Object([]string{"path"}, map[string]mcp.Schema{
				"path": mcp.String("Absolute path of the file."),
			}),
			Annotations: modifying("Open document"),
			Handler: handler(3*defaultTimeout, func(ctx context.Context, a *openDocArgs) (*mcp.Result, error) {
				return openDocument(ctx, d, a)
			}),
		},
		{
			Name:        "save_document",
			Toolset:     "app",
			Description: "Save a document to its file. A document that was never saved needs save_document_as.",
			InputSchema: mcp.Object(nil, docRefSchema(map[string]mcp.Schema{})),
			Annotations: modifying("Save document"),
			Handler: handler(3*defaultTimeout, func(ctx context.Context, a *docRef) (*mcp.Result, error) {
				return docOp(ctx, d, a, func(c *corel.Ctx, doc *com.Dispatch) (string, error) {
					file, _ := doc.GetString("FullFileName")
					if file == "" {
						return "", errors.New("the document has no file yet – use save_document_as with a path")
					}
					if _, err := doc.Call("Save"); err != nil {
						return "", err
					}
					return "Saved to " + file, nil
				})
			}),
		},
		{
			Name:    "save_document_as",
			Toolset: "app",
			Description: "Save a document as a CorelDRAW .cdr file under a new path, optionally in an older CDR version " +
				"(version: 0 = current, 27 = 2025/2026, 26 = 2024, 25 = 2023, 24 = 2022, 23 = 2021, 22 = 2020, 21 = 2019, 20 = 2018, 19 = 2017, 17 = X7). " +
				"For other formats use export.",
			InputSchema: mcp.Object([]string{"path"}, docRefSchema(map[string]mcp.Schema{
				"path":      mcp.String(`Absolute path ending in .cdr.`),
				"version":   mcp.Integer("CDR file version (0 = current)."),
				"overwrite": mcp.Boolean("Overwrite an existing file."),
			})),
			Annotations: destructive("Save document as"),
			Handler: handler(3*defaultTimeout, func(ctx context.Context, a *saveAsArgs) (*mcp.Result, error) {
				return saveDocumentAs(ctx, d, a)
			}),
		},
		{
			Name:    "close_document",
			Toolset: "app",
			Description: "Close a document. If it has unsaved changes you must choose save: true (saves to its file) or discard: true " +
				"(changes are lost – ask the user first).",
			InputSchema: mcp.Object(nil, docRefSchema(map[string]mcp.Schema{
				"save":    mcp.Boolean("Save before closing."),
				"discard": mcp.Boolean("Close without saving, discarding changes."),
			})),
			Annotations: destructive("Close document"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *closeDocArgs) (*mcp.Result, error) {
				return closeDocument(ctx, d, a)
			}),
		},
	}
}

// findDocument resolves a document by number or name; the active one when both are empty.
func findDocument(c *corel.Ctx, r *docRef) (*com.Dispatch, error) {
	if r.Document == 0 && r.Name == "" {
		return c.ActiveDocument()
	}
	docs, err := c.App.GetObj("Documents")
	if err != nil {
		return nil, err
	}
	list, n, err := corel.Collect(docs, 0)
	if err != nil {
		return nil, err
	}
	if r.Document != 0 {
		if r.Document < 1 || r.Document > n {
			return nil, fmt.Errorf("document %d does not exist (%d open)", r.Document, n)
		}
		return list[r.Document-1], nil
	}
	var names []string
	for _, doc := range list {
		name, _ := doc.GetString("Name")
		if strings.EqualFold(name, r.Name) || strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), r.Name) {
			return doc, nil
		}
		names = append(names, name)
	}
	return nil, fmt.Errorf("document %q is not open (open: %s)", r.Name, strings.Join(names, ", "))
}

func docOp(ctx context.Context, d *Deps, r *docRef, fn func(c *corel.Ctx, doc *com.Dispatch) (string, error)) (*mcp.Result, error) {
	var msg string
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := findDocument(c, r)
		if err != nil {
			return err
		}
		msg, err = fn(c, doc)
		return err
	})
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(msg), nil
}

func describeDoc(doc *com.Dispatch, index int, activeName string) documentInfo {
	di := documentInfo{Index: index}
	di.Name, _ = doc.GetString("Name")
	di.File, _ = doc.GetString("FullFileName")
	if pages, err := doc.GetObj("Pages"); err == nil {
		di.Pages, _ = pages.GetInt("Count")
	}
	di.Dirty, _ = doc.GetBool("Dirty")
	di.Active = di.Name == activeName
	if a, err := doc.GetBool("Active"); err == nil {
		di.Active = a
	}
	return di
}

func listDocuments(ctx context.Context, d *Deps) (*mcp.Result, error) {
	var out []documentInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		docs, err := c.App.GetObj("Documents")
		if err != nil {
			return err
		}
		list, _, err := corel.Collect(docs, 0)
		if err != nil {
			return err
		}
		active := ""
		if ad, err := c.App.GetObjOrNil("ActiveDocument"); err == nil && ad != nil {
			active, _ = ad.GetString("Name")
		}
		for i, doc := range list {
			out = append(out, describeDoc(doc, i+1, active))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("%d document(s) open.", len(out)), map[string]any{"documents": out}), nil
}

func newDocument(ctx context.Context, d *Deps, a *newDocArgs) (*mcp.Result, error) {
	if a.Path != "" {
		if !filepath.IsAbs(a.Path) {
			return nil, fmt.Errorf("path must be absolute, got %q", a.Path)
		}
		if !strings.EqualFold(filepath.Ext(a.Path), ".cdr") {
			return nil, errors.New("path must end with .cdr")
		}
		if fileExists(a.Path) && !a.Overwrite {
			return nil, fmt.Errorf("%s already exists – set overwrite: true to replace it", a.Path)
		}
		if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
			return nil, err
		}
	}
	w, h := f64(a.Width, 210), f64(a.Height, 297)
	if w <= 0 || h <= 0 {
		return nil, errors.New("width and height must be greater than 0")
	}
	if a.Pages == 0 {
		a.Pages = 1
	}
	if a.Pages < 1 || a.Pages > 999 {
		return nil, errors.New("pages must be 1 – 999")
	}
	var info documentInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		c.Logf("new_document: CreateDocument")
		doc, err := c.App.CallObj("CreateDocument")
		if err != nil {
			return err
		}
		c.Logf("new_document: created, setting units")
		return c.WithMM(doc, func() error {
			c.Logf("new_document: SetSize")
			page, err := doc.GetObj("ActivePage")
			if err != nil {
				return err
			}
			if _, err := page.Call("SetSize", w, h); err != nil {
				return err
			}
			if a.Pages > 1 {
				c.Logf("new_document: AddPagesEx")
				if _, err := doc.Call("AddPagesEx", a.Pages-1, w, h); err != nil {
					return err
				}
				if first, _, err := c.Page(doc, 1); err == nil {
					_, _ = first.Call("Activate")
				}
			}
			// Document.Name is not set: assigning it freezes CorelDRAW 2026.
			// A name comes from saving the document instead.
			if a.Path != "" {
				c.Logf("new_document: SaveAs")
				opts, err := c.App.CallObj("CreateStructSaveAsOptions")
				if err != nil {
					return err
				}
				for _, p := range []struct {
					n string
					v any
				}{{"Filter", cdrCDR}, {"Overwrite", true}, {"Range", 0}} {
					if err := opts.Put(p.n, p.v); err != nil {
						return fmt.Errorf("save option %s: %w", p.n, err)
					}
				}
				if _, err := doc.Call("SaveAs", a.Path, opts); err != nil {
					return fmt.Errorf("document created, but saving to %s failed: %w", a.Path, err)
				}
			}
			c.Logf("new_document: done")
			_ = doc.Put("Dirty", false)
			info = describeDoc(doc, 0, "")
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("New document %q (%g × %g mm, %d page(s)).", info.Name, w, h, a.Pages), info), nil
}

func openDocument(ctx context.Context, d *Deps, a *openDocArgs) (*mcp.Result, error) {
	if !filepath.IsAbs(a.Path) {
		return nil, fmt.Errorf("path must be absolute, got %q", a.Path)
	}
	if !fileExists(a.Path) {
		return nil, fmt.Errorf("file not found: %s", a.Path)
	}
	var info documentInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.App.CallObj("OpenDocument", a.Path, 0)
		if err != nil {
			return err
		}
		info = describeDoc(doc, 0, "")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Opened %q (%d page(s)).", info.Name, info.Pages), info), nil
}

func saveDocumentAs(ctx context.Context, d *Deps, a *saveAsArgs) (*mcp.Result, error) {
	if !filepath.IsAbs(a.Path) {
		return nil, fmt.Errorf("path must be absolute, got %q", a.Path)
	}
	if !strings.EqualFold(filepath.Ext(a.Path), ".cdr") {
		return nil, errors.New("save_document_as writes .cdr files; use export for other formats")
	}
	if fileExists(a.Path) && !a.Overwrite {
		return nil, fmt.Errorf("%s already exists – set overwrite: true to replace it", a.Path)
	}
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
		return nil, err
	}
	return docOp(ctx, d, &a.docRef, func(c *corel.Ctx, doc *com.Dispatch) (string, error) {
		opts, err := c.App.CallObj("CreateStructSaveAsOptions")
		if err != nil {
			return "", err
		}
		for _, p := range []struct {
			n string
			v any
		}{{"Filter", cdrCDR}, {"Version", a.Version}, {"Overwrite", true}, {"Range", 0}} {
			if err := opts.Put(p.n, p.v); err != nil {
				return "", fmt.Errorf("save option %s: %w", p.n, err)
			}
		}
		if _, err := doc.Call("SaveAs", a.Path, opts); err != nil {
			return "", err
		}
		return "Saved as " + a.Path, nil
	})
}

func closeDocument(ctx context.Context, d *Deps, a *closeDocArgs) (*mcp.Result, error) {
	if a.Save && a.Discard {
		return nil, errors.New("choose save or discard, not both")
	}
	return docOp(ctx, d, &a.docRef, func(c *corel.Ctx, doc *com.Dispatch) (string, error) {
		name, _ := doc.GetString("Name")
		dirty, _ := doc.GetBool("Dirty")
		if dirty {
			switch {
			case a.Save:
				file, _ := doc.GetString("FullFileName")
				if file == "" {
					return "", errors.New("the document was never saved – use save_document_as first, or discard: true")
				}
				if _, err := doc.Call("Save"); err != nil {
					return "", err
				}
			case a.Discard:
				if err := doc.Put("Dirty", false); err != nil {
					return "", err
				}
			default:
				return "", fmt.Errorf("document %q has unsaved changes – use save: true or discard: true", name)
			}
		}
		if _, err := doc.Call("Close"); err != nil {
			return "", err
		}
		return fmt.Sprintf("Document %q closed.", name), nil
	})
}

// --- pages ---------------------------------------------------------------

type pageInfo struct {
	Page        int     `json:"page"`
	Name        string  `json:"name,omitempty"`
	Width       float64 `json:"width_mm"`
	Height      float64 `json:"height_mm"`
	Orientation string  `json:"orientation"`
	Shapes      int64   `json:"shapes"`
	Active      bool    `json:"active,omitempty"`
}

type addPagesArgs struct {
	Count  int      `json:"count"`
	After  *int     `json:"after"`
	Width  *float64 `json:"width"`
	Height *float64 `json:"height"`
}

type deletePageArgs struct {
	Page  int `json:"page"`
	Count int `json:"count"`
}

type setPageArgs struct {
	Page        int      `json:"page"`
	Width       *float64 `json:"width"`
	Height      *float64 `json:"height"`
	Orientation string   `json:"orientation"`
	Name        *string  `json:"name"`
}

type pageArg struct {
	Page int `json:"page"`
}

func pageTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:        "list_pages",
			Toolset:     "pages",
			Description: "List the pages of the active document: number, name, size in mm, orientation, shape count, active page.",
			InputSchema: mcp.Object(nil, nil),
			Annotations: readOnly("List pages"),
			Handler: handler(defaultTimeout, func(ctx context.Context, _ *struct{}) (*mcp.Result, error) {
				return listPages(ctx, d)
			}),
		},
		{
			Name:        "add_pages",
			Toolset:     "pages",
			Description: "Add pages to the active document – at the end, or after page `after` (0 = at the beginning). Size defaults to the active page's size.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"count":  mcp.Default(mcp.Range(mcp.Integer("Number of pages to add."), 1, 999), 1),
				"after":  mcp.Integer("Insert after this page number (0 = before the first page). Default: at the end."),
				"width":  mcp.Number("Page width in mm."),
				"height": mcp.Number("Page height in mm."),
			}),
			Annotations: modifying("Add pages"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *addPagesArgs) (*mcp.Result, error) {
				return addPages(ctx, d, a)
			}),
		},
		{
			Name:        "delete_page",
			Toolset:     "pages",
			Description: "Delete a page (and everything on it) – or count pages starting at it. The last remaining page cannot be deleted.",
			InputSchema: mcp.Object([]string{"page"}, map[string]mcp.Schema{
				"page":  mcp.Integer("Page number."),
				"count": mcp.Default(mcp.Integer("Number of pages to delete."), 1),
			}),
			Annotations: destructive("Delete page"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *deletePageArgs) (*mcp.Result, error) {
				return deletePage(ctx, d, a)
			}),
		},
		{
			Name:        "set_page",
			Toolset:     "pages",
			Description: "Change a page: size (mm), orientation (swaps width and height) and/or name.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"page":        mcp.Integer("Page number. Default: the active page."),
				"width":       mcp.Number("Width in mm."),
				"height":      mcp.Number("Height in mm."),
				"orientation": mcp.Enum("Page orientation.", "portrait", "landscape"),
				"name":        mcp.String("Page name."),
			}),
			Annotations: modifying("Set page"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *setPageArgs) (*mcp.Result, error) {
				return setPage(ctx, d, a)
			}),
		},
		{
			Name:        "activate_page",
			Toolset:     "pages",
			Description: "Switch CorelDRAW to another page.",
			InputSchema: mcp.Object([]string{"page"}, map[string]mcp.Schema{
				"page": mcp.Integer("Page number."),
			}),
			Annotations: modifying("Activate page"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *pageArg) (*mcp.Result, error) {
				err := d.S.Run(ctx, func(c *corel.Ctx) error {
					doc, err := c.ActiveDocument()
					if err != nil {
						return err
					}
					p, _, err := c.Page(doc, a.Page)
					if err != nil {
						return err
					}
					_, err = p.Call("Activate")
					return err
				})
				if err != nil {
					return nil, err
				}
				return mcp.TextResult(fmt.Sprintf("Page %d is active.", a.Page)), nil
			}),
		},
	}
}

func describePage(p *com.Dispatch, active int) pageInfo {
	var pi pageInfo
	idx, _ := p.GetInt("Index")
	pi.Page = int(idx)
	pi.Name, _ = p.GetString("Name")
	w, _ := p.GetFloat("SizeWidth")
	h, _ := p.GetFloat("SizeHeight")
	pi.Width, pi.Height = geom.Round(w), geom.Round(h)
	pi.Orientation = "portrait"
	if o, _ := p.GetInt("Orientation"); o == 1 {
		pi.Orientation = "landscape"
	}
	if sh, err := p.GetObj("Shapes"); err == nil {
		pi.Shapes, _ = sh.GetInt("Count")
	}
	pi.Active = pi.Page == active
	return pi
}

func listPages(ctx context.Context, d *Deps) (*mcp.Result, error) {
	var out []pageInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		return c.WithMM(doc, func() error {
			_, active, err := c.Page(doc, 0)
			if err != nil {
				return err
			}
			pages, err := doc.GetObj("Pages")
			if err != nil {
				return err
			}
			list, _, err := corel.Collect(pages, 0)
			if err != nil {
				return err
			}
			for _, p := range list {
				out = append(out, describePage(p, active))
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("%d page(s).", len(out)), map[string]any{"pages": out}), nil
}

func addPages(ctx context.Context, d *Deps, a *addPagesArgs) (*mcp.Result, error) {
	if a.Count == 0 {
		a.Count = 1
	}
	if a.Count < 1 || a.Count > 999 {
		return nil, errors.New("count must be 1 – 999")
	}
	var first pageInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		return c.WithMM(doc, func() error {
			ap, active, err := c.Page(doc, 0)
			if err != nil {
				return err
			}
			w0, _ := ap.GetFloat("SizeWidth")
			h0, _ := ap.GetFloat("SizeHeight")
			w, h := f64(a.Width, w0), f64(a.Height, h0)
			return c.Write(doc, "add_pages", func() error {
				var p *com.Dispatch
				if a.After == nil {
					p, err = doc.CallObj("AddPagesEx", a.Count, w, h)
				} else if *a.After == 0 {
					p, err = doc.CallObj("InsertPagesEx", a.Count, true, 1, w, h)
				} else {
					if _, _, err := c.Page(doc, *a.After); err != nil {
						return err
					}
					p, err = doc.CallObj("InsertPagesEx", a.Count, false, *a.After, w, h)
				}
				if err != nil {
					return err
				}
				first = describePage(p, active)
				return nil
			})
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Added %d page(s), first new page is %d.", a.Count, first.Page), first), nil
}

func deletePage(ctx context.Context, d *Deps, a *deletePageArgs) (*mcp.Result, error) {
	if a.Count == 0 {
		a.Count = 1
	}
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		pages, err := doc.GetObj("Pages")
		if err != nil {
			return err
		}
		n, _ := pages.GetInt("Count")
		if a.Page < 1 || int64(a.Page+a.Count-1) > n {
			return fmt.Errorf("pages %d–%d do not exist (the document has %d)", a.Page, a.Page+a.Count-1, n)
		}
		if int64(a.Count) >= n {
			return errors.New("cannot delete all pages of a document")
		}
		return c.Write(doc, "delete_page", func() error {
			_, err := doc.Call("DeletePages", a.Page, a.Count)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(fmt.Sprintf("Deleted %d page(s) starting at page %d.", a.Count, a.Page)), nil
}

func setPage(ctx context.Context, d *Deps, a *setPageArgs) (*mcp.Result, error) {
	or := map[string]int{"portrait": 0, "landscape": 1}
	if _, ok := or[a.Orientation]; a.Orientation != "" && !ok {
		return nil, fmt.Errorf("unknown orientation %q", a.Orientation)
	}
	var pi pageInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		return c.WithMM(doc, func() error {
			p, _, err := c.Page(doc, a.Page)
			if err != nil {
				return err
			}
			_, active, _ := c.Page(doc, 0)
			return c.Write(doc, "set_page", func() error {
				if a.Width != nil || a.Height != nil {
					w, _ := p.GetFloat("SizeWidth")
					h, _ := p.GetFloat("SizeHeight")
					w, h = f64(a.Width, w), f64(a.Height, h)
					if w <= 0 || h <= 0 {
						return errors.New("width and height must be greater than 0")
					}
					if _, err := p.Call("SetSize", w, h); err != nil {
						return err
					}
				}
				if a.Orientation != "" {
					if err := p.Put("Orientation", or[a.Orientation]); err != nil {
						return err
					}
				}
				if a.Name != nil {
					if err := p.Put("Name", *a.Name); err != nil {
						return err
					}
				}
				pi = describePage(p, active)
				return nil
			})
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Page %d: %g × %g mm.", pi.Page, pi.Width, pi.Height), pi), nil
}

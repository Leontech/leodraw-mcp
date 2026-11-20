//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type copyArgs struct {
	targetArgs
	TargetPage int      `json:"target_page"`
	Layer      string   `json:"layer"`
	X          *float64 `json:"x"`
	Y          *float64 `json:"y"`
	Anchor     string   `json:"anchor"`
	DX         float64  `json:"dx"`
	DY         float64  `json:"dy"`
	Rotate     float64  `json:"rotate"`
	Move       bool     `json:"move"`
}

type templateArgs struct {
	Values        map[string]string `json:"values"`
	Open          string            `json:"open"`
	Close         string            `json:"close"`
	Page          int               `json:"page"`
	AllPages      bool              `json:"all_pages"`
	IDs           []int64           `json:"ids"`
	ListOnly      bool              `json:"list_only"`
	CaseSensitive bool              `json:"case_sensitive"`
}

func moreTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "copy_shapes",
			Toolset: "transform",
			Description: "Copy (or with move: true move) shapes (ids or selection) to another page and/or layer in one step – they keep " +
				"their position on the page unless x/y (anchor point), dx/dy or rotate (degrees, counter-clockwise) are given. " +
				"Returns the new shapes.",
			InputSchema: mcp.Object(nil, targetSchema(map[string]mcp.Schema{
				"target_page": mcp.Integer("Page to copy to (1-based). Default: the same page."),
				"layer":       mcp.String("Layer on the target page. Default: the target page's first (top) layer of the same name as the source, else the active layer."),
				"x":           mcp.Number("Place the copy so that its anchor point is at x (mm)."),
				"y":           mcp.Number("Place the copy so that its anchor point is at y (mm)."),
				"anchor":      anchorSchema(),
				"dx":          mcp.Number("Then move right by mm."),
				"dy":          mcp.Number("Then move down by mm."),
				"rotate":      mcp.Number("Rotate the copy (degrees, counter-clockwise, around its centre) before placing it."),
				"move":        mcp.Boolean("Move the shapes instead of copying them."),
			})),
			Annotations: modifying("Copy shapes"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *copyArgs) (*mcp.Result, error) {
				return copyShapes(ctx, d, a)
			}),
		},
		{
			Name:    "fill_template",
			Toolset: "text",
			Description: "Fill placeholders like {{name}} in text shapes with values – e.g. a label template with {{VIN}}, {{weight}}. " +
				"values maps placeholder names to texts. Works on the active page (or page / all_pages / ids). Formatting is kept. " +
				"list_only: true just lists the placeholders found. Reports placeholders left unfilled.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"values":         {"type": "object", "description": `Placeholder name → value, e.g. {"VIN": "VAVJS1339FH354618"}.`, "additionalProperties": map[string]any{"type": "string"}},
				"open":           mcp.String(`Placeholder start (default "{{").`),
				"close":          mcp.String(`Placeholder end (default "}}").`),
				"page":           mcp.Integer("Page number. Default: the active page."),
				"all_pages":      mcp.Boolean("All pages of the document."),
				"ids":            mcp.Array("Only these text shapes (on page).", mcp.Integer("id")),
				"list_only":      mcp.Boolean("Only list the placeholders, change nothing."),
				"case_sensitive": mcp.Boolean("Placeholder names are case-sensitive (default false)."),
			}),
			Annotations: modifying("Fill template"),
			Handler: handler(3*defaultTimeout, func(ctx context.Context, a *templateArgs) (*mcp.Result, error) {
				return fillTemplate(ctx, d, a)
			}),
		},
	}
}

// --- copy_shapes ------------------------------------------------------------

func copyShapes(ctx context.Context, d *Deps, a *copyArgs) (*mcp.Result, error) {
	if _, err := anchorFrac(a.Anchor); err != nil {
		return nil, err
	}
	op := "copy_shapes"
	if a.Move {
		op = "move_shapes"
	}
	var infos []corel.ShapeInfo
	target := 0
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, op, func(p *pageCtx) error {
			_, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			tp, tidx := p.page, p.index
			if a.TargetPage != 0 {
				if tp, tidx, err = c.Page(p.doc, a.TargetPage); err != nil {
					return err
				}
			}
			target = tidx
			tf, err := c.Frame(tp)
			if err != nil {
				return err
			}
			var out []*com.Dispatch
			for _, s := range list {
				layer, err := targetLayer(c, p.doc, tp, s, a.Layer)
				if err != nil {
					return err
				}
				bb, err := corel.BBox(s, p.f) // position on the source page
				if err != nil {
					return err
				}
				var n *com.Dispatch
				if a.Move {
					if _, err := s.Call("MoveToLayer", layer); err != nil {
						return err
					}
					n = s
				} else if n, err = s.CallObj("CopyToLayer", layer); err != nil {
					return err
				}
				// keep the page-relative position (pages may differ in size or origin)
				if err := placeAt(n, tf, bb.X, bb.Y, "top-left"); err != nil {
					return err
				}
				out = append(out, n)
			}
			obj := out[0]
			if len(out) > 1 {
				if obj, err = newRange(c, out); err != nil {
					return err
				}
			}
			if a.Rotate != 0 {
				cx, cy, err := center(obj)
				if err != nil {
					return err
				}
				if _, err := obj.Call("RotateEx", a.Rotate, cx, cy); err != nil {
					return err
				}
			}
			if err := placeAtOpt(obj, tf, a.X, a.Y, a.Anchor); err != nil {
				return err
			}
			if err := moveBy(obj, tf, a.DX, a.DY); err != nil {
				return err
			}
			for _, s := range out {
				if si, err := c.DescribeShape(s, tf, "", 0); err == nil {
					infos = append(infos, si)
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	verb := "Copied"
	if a.Move {
		verb = "Moved"
	}
	return mcp.JSONResult(fmt.Sprintf("%s %d shape(s) to page %d.", verb, len(infos), target),
		map[string]any{"page": target, "shapes": infos}), nil
}

// targetLayer picks the layer on page tp: the named one, else the layer with
// the same name as the shape's layer, else the active / first layer.
func targetLayer(c *corel.Ctx, doc, tp, s *com.Dispatch, name string) (*com.Dispatch, error) {
	if name != "" {
		return c.Layer(doc, tp, name)
	}
	if l, err := s.GetObj("Layer"); err == nil {
		if ln, _ := l.GetString("Name"); ln != "" {
			if tl, err := c.Layer(doc, tp, ln); err == nil {
				return tl, nil
			}
		}
	}
	if al, err := tp.GetObj("ActiveLayer"); err == nil && al != nil {
		return al, nil
	}
	layers, err := tp.GetObj("Layers")
	if err != nil {
		return nil, err
	}
	return layers.GetObj("Item", 1)
}

// --- fill_template ----------------------------------------------------------

func fillTemplate(ctx context.Context, d *Deps, a *templateArgs) (*mcp.Result, error) {
	open, close := a.Open, a.Close
	if open == "" {
		open = "{{"
	}
	if close == "" {
		close = "}}"
	}
	if !a.ListOnly && len(a.Values) == 0 {
		return nil, errors.New("give values (placeholder → text), or list_only: true")
	}
	rx := regexp.MustCompile(regexp.QuoteMeta(open) + `\s*([^\r\n]+?)\s*` + regexp.QuoteMeta(close))
	norm := func(k string) string {
		if a.CaseSensitive {
			return k
		}
		return strings.ToLower(k)
	}
	values := map[string]string{}
	for k, v := range a.Values {
		values[norm(strings.TrimSpace(k))] = v
	}
	found := map[string]int{}
	replaced, shapesChanged := 0, 0
	op := "fill_template"
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		var texts []*com.Dispatch
		collect := func(page *com.Dispatch) {
			layers, err := page.GetObj("Layers")
			if err != nil {
				return
			}
			ls, _, _ := corel.Collect(layers, 0)
			for _, l := range ls {
				if sh, err := l.GetObj("Shapes"); err == nil {
					walkShapes(sh, 0, 20, func(s *com.Dispatch, _ int) bool {
						if t, _ := s.GetInt("Type"); t == 6 {
							texts = append(texts, s)
						}
						return true
					})
				}
			}
		}
		switch {
		case len(a.IDs) > 0:
			page, _, err := c.Page(doc, a.Page)
			if err != nil {
				return err
			}
			for _, id := range a.IDs {
				s, err := corel.FindShape(page, id)
				if err != nil {
					return err
				}
				texts = append(texts, s)
			}
		case a.AllPages:
			pages, err := doc.GetObj("Pages")
			if err != nil {
				return err
			}
			ps, _, _ := corel.Collect(pages, 0)
			for _, pg := range ps {
				collect(pg)
			}
		default:
			page, _, err := c.Page(doc, a.Page)
			if err != nil {
				return err
			}
			collect(page)
		}
		work := func() error {
			for _, s := range texts {
				_, st, err := story(s)
				if err != nil {
					continue
				}
				txt, _ := st.GetString("Text")
				ms := rx.FindAllStringSubmatchIndex(txt, -1)
				if len(ms) == 0 {
					continue
				}
				changed := false
				// replace from the end so that earlier indexes stay valid
				for i := len(ms) - 1; i >= 0; i-- {
					m := ms[i]
					key := norm(txt[m[2]:m[3]])
					found[key]++
					v, ok := values[key]
					if !ok || a.ListOnly {
						continue
					}
					start := len([]rune(txt[:m[0]]))
					end := len([]rune(txt[:m[1]]))
					rng, err := s.GetObj("Text")
					if err != nil {
						return err
					}
					r, err := rng.CallObj("Range", start, end)
					if err != nil {
						return err
					}
					if err := r.Put("Text", crText(v)); err != nil {
						return err
					}
					replaced++
					changed = true
				}
				if changed {
					shapesChanged++
				}
			}
			return nil
		}
		if a.ListOnly {
			return work()
		}
		return c.Write(doc, op, work)
	})
	if err != nil {
		return nil, err
	}
	var placeholders, missing []string
	for k := range found {
		placeholders = append(placeholders, k)
		if _, ok := values[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(placeholders)
	sort.Strings(missing)
	var unused []string
	for k := range values {
		if found[k] == 0 {
			unused = append(unused, k)
		}
	}
	sort.Strings(unused)
	res := map[string]any{"placeholders": placeholders, "replaced": replaced, "shapes_changed": shapesChanged}
	if len(missing) > 0 {
		res["unfilled"] = missing
	}
	if len(unused) > 0 {
		res["values_not_found"] = unused
	}
	msg := fmt.Sprintf("Filled %d placeholder(s) in %d text shape(s).", replaced, shapesChanged)
	if a.ListOnly {
		msg = fmt.Sprintf("%d placeholder name(s) found.", len(placeholders))
	}
	return mcp.JSONResult(msg, res), nil
}

// --- batch ------------------------------------------------------------------

type batchStep struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type batchArgs struct {
	Steps       []batchStep `json:"steps"`
	StopOnError *bool       `json:"stop_on_error"`
	Verbose     bool        `json:"verbose"`
}

type batchResult struct {
	Step  int    `json:"step"`
	Tool  string `json:"tool"`
	OK    bool   `json:"ok"`
	Text  string `json:"result,omitempty"`
	Error string `json:"error,omitempty"`
}

// batchTool runs several tool calls in one request and, as far as CorelDRAW
// allows nesting, as one undo step.
func batchTool(srv *mcp.Server, d *Deps) *mcp.Tool {
	return &mcp.Tool{
		Name:    "batch",
		Toolset: "app",
		Description: "Run several LeoDraw tool calls in one request – much faster than separate calls, and the whole batch is " +
			"one undo step in CorelDRAW. steps = [{\"tool\": \"set_text\", \"arguments\": {…}}, …], run in order. " +
			"Stops at the first error unless stop_on_error is false. Not for render_preview (images are dropped).",
		InputSchema: mcp.Object([]string{"steps"}, map[string]mcp.Schema{
			"steps": mcp.Array("Tool calls in order.", mcp.Object([]string{"tool"}, map[string]mcp.Schema{
				"tool":      mcp.String("Tool name, e.g. \"set_text\"."),
				"arguments": {"type": "object", "description": "Arguments of the tool."},
			})),
			"stop_on_error": mcp.Boolean("Stop at the first failing step (default true)."),
			"verbose":       mcp.Boolean("Return each step's full result (JSON) instead of its one-line summary."),
		}),
		Annotations: &mcp.Annotations{Title: "Batch"},
		Handler: func(ctx context.Context, raw json.RawMessage) (*mcp.Result, error) {
			var a batchArgs
			if err := mcp.Bind(raw, &a); err != nil {
				return nil, err
			}
			if len(a.Steps) == 0 {
				return nil, errors.New("steps is empty")
			}
			if len(a.Steps) > 500 {
				return nil, errors.New("at most 500 steps per batch")
			}
			stop := a.StopOnError == nil || *a.StopOnError
			for i, st := range a.Steps {
				if t := srv.Tool(st.Tool); t == nil || st.Tool == "batch" || st.Tool == "call_tool" || st.Tool == "server_reload" {
					return nil, fmt.Errorf("step %d: unknown or not allowed tool %q", i+1, st.Tool)
				}
			}
			ctx, cancel := context.WithTimeout(ctx, time.Duration(len(a.Steps))*defaultTimeout)
			defer cancel()
			// Outer undo group: inner tools open their own groups inside it.
			grouped := false
			d.S.SetBatch(true) // keep the connection (and with it the undo group) open
			_ = d.S.Run(ctx, func(c *corel.Ctx) error {
				if doc, err := c.ActiveDocument(); err == nil {
					if _, err := doc.Call("BeginCommandGroup", "LeoDraw: batch"); err == nil {
						grouped = true
					}
				}
				return nil
			})
			defer func() {
				_ = d.S.Run(context.Background(), func(c *corel.Ctx) error {
					if grouped {
						if doc, err := c.ActiveDocument(); err == nil {
							_, _ = doc.Call("EndCommandGroup")
						}
					}
					_, _ = c.App.Call("Refresh")
					d.S.SetBatch(false) // the reference is dropped after this job
					return nil
				})
			}()
			if !grouped {
				d.S.SetBatch(false)
			}
			var results []batchResult
			failed := 0
			for i, st := range a.Steps {
				r := batchResult{Step: i + 1, Tool: st.Tool}
				args := st.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				res, err := srv.Tool(st.Tool).Handler(ctx, args)
				switch {
				case err != nil:
					r.Error = err.Error()
				case res != nil && res.IsError:
					r.Error = res.FirstText()
				default:
					r.OK = true
					if res != nil {
						r.Text = res.FirstText()
						if !a.Verbose {
							r.Text, _, _ = strings.Cut(r.Text, "\n")
						}
					}
				}
				results = append(results, r)
				if !r.OK {
					failed++
					if stop {
						break
					}
				}
			}
			msg := fmt.Sprintf("Batch: %d step(s) done, %d failed.", len(results)-failed, failed)
			if failed > 0 && stop && len(results) < len(a.Steps) {
				msg += fmt.Sprintf(" Stopped at step %d; the steps before it were applied (use undo to revert).", len(results))
			}
			out := mcp.JSONResult(msg, map[string]any{"steps": results})
			out.IsError = failed > 0 && stop
			return out, nil
		},
	}
}

//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools defines the MCP tools of LeoDraw.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

// Deps are shared by all tools.
type Deps struct {
	S           *corel.Session
	Log         *log.Logger
	Toolsets    map[string]bool // nil = all default toolsets
	AllowRawCOM bool
	AllowMacros bool
	// Supervised: running as the child of the supervisor, server_reload is available.
	Supervised bool
}

// Toolsets lists all toolset names with a short description.
var Toolsets = map[string]string{
	"app":        "application status, documents, undo/redo",
	"pages":      "pages",
	"layers":     "layers",
	"inspect":    "reading, searching and selecting shapes",
	"preview":    "rendering PNG previews",
	"draw":       "creating shapes and importing",
	"text":       "text",
	"transform":  "move, size, rotate, align, distribute, duplicate, order, delete",
	"style":      "fill and outline",
	"shaping":    "group, combine, convert to curves",
	"export":     "export to PDF, SVG, DXF, PNG …",
	"bitmap":     "bitmaps (PowerTRACE vectorizing)",
	"production": "preflight and preparing for laser / plotter cutting",
}

// About is shown by corel_status and --version.
var About = map[string]string{
	"name":     "LeoDraw MCP",
	"author":   "Leoš Vilím",
	"web":      "https://leotech.cz",
	"source":   "https://github.com/Leontech/leodraw-mcp",
	"support":  "https://ko-fi.com/leotechcz",
	"feedback": "https://leotech.cz/download?fb=leodraw",
	"license":  "GPL-3.0-or-later",
	"notice":   "Unofficial project, not affiliated with Corel Corporation or Anthropic.",
}

const defaultTimeout = 60 * time.Second

func readOnly(title string) *mcp.Annotations {
	return &mcp.Annotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
}

func modifying(title string) *mcp.Annotations {
	return &mcp.Annotations{Title: title}
}

func destructive(title string) *mcp.Annotations {
	return &mcp.Annotations{Title: title, DestructiveHint: true}
}

// handler adapts a typed function to mcp.Handler with argument binding and a timeout.
func handler[A any](timeout time.Duration, fn func(ctx context.Context, a *A) (*mcp.Result, error)) mcp.Handler {
	return func(ctx context.Context, raw json.RawMessage) (*mcp.Result, error) {
		a := new(A)
		if err := mcp.Bind(raw, a); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		res, err := fn(ctx, a)
		if err != nil {
			return nil, friendly(err)
		}
		return res, nil
	}
}

func friendly(err error) error {
	if errors.Is(err, corel.ErrNotRunning) || errors.Is(err, corel.ErrNoDocument) {
		return err
	}
	msg := err.Error()
	if (strings.Contains(msg, "did not finish in time") || strings.Contains(msg, "still busy")) && corel.WindowHung() {
		return errors.New(msg + " – CorelDRAW is NOT RESPONDING (its window is hung). Tell the user: wait a moment; if it stays " +
			"frozen, end CorelDRW.exe in Task Manager (unsaved changes are lost) and start CorelDRAW again")
	}
	if strings.Contains(msg, "0x80010001") || strings.Contains(msg, "0x8001010A") {
		return errors.New(msg + " – CorelDRAW is busy; close any open dialog in CorelDRAW and try again")
	}
	return err
}

// Register adds all enabled tools to srv.
func Register(srv *mcp.Server, d *Deps) {
	all := []*mcp.Tool{}
	all = append(all, appTools(d)...)
	all = append(all, inspectTools(d)...)
	all = append(all, previewTools(d)...)
	all = append(all, documentTools(d)...)
	all = append(all, pageTools(d)...)
	all = append(all, layerTools(d)...)
	all = append(all, inspect2Tools(d)...)
	all = append(all, drawTools(d)...)
	all = append(all, draw2Tools(d)...)
	all = append(all, textTools(d)...)
	all = append(all, transformTools(d)...)
	all = append(all, styleTools(d)...)
	all = append(all, exportTools(d)...)
	all = append(all, moreTools(d)...)
	all = append(all, phase2Tools(d)...)
	all = append(all, describeTools(d)...)
	for _, t := range all {
		if d.Toolsets != nil && !d.Toolsets[t.Toolset] {
			continue
		}
		srv.AddTool(t)
	}
	srv.AddTool(callTool(srv))
	srv.AddTool(batchTool(srv, d))
}

type callToolArgs struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// callTool lets a client reach tools that are missing from its cached tool
// list (some clients do not reload the list after server_reload).
func callTool(srv *mcp.Server) *mcp.Tool {
	return &mcp.Tool{
		Name:    "call_tool",
		Toolset: "app",
		Description: "Call another LeoDraw tool by name with its arguments. Use it ONLY when a LeoDraw tool you need is not in your tool list " +
			"(clients may keep an old tool list after an update). Unknown names return the list of available tools.",
		InputSchema: mcp.Object([]string{"name"}, map[string]mcp.Schema{
			"name":      mcp.String(`Tool name, e.g. "create_ellipse".`),
			"arguments": mcp.Schema{"type": "object", "description": "Arguments of that tool."},
		}),
		Annotations: &mcp.Annotations{Title: "Call LeoDraw tool"},
		Handler: func(ctx context.Context, raw json.RawMessage) (*mcp.Result, error) {
			var a callToolArgs
			if err := mcp.Bind(raw, &a); err != nil {
				return nil, err
			}
			t := srv.Tool(a.Name)
			if t == nil || a.Name == "call_tool" {
				var names []string
				for _, t := range srv.Tools() {
					if t.Name != "call_tool" {
						names = append(names, t.Name)
					}
				}
				return nil, errors.New("unknown tool " + strconv.Quote(a.Name) + "; available: " + strings.Join(names, ", "))
			}
			return t.Handler(ctx, a.Arguments)
		},
	}
}

// Instructions are sent to the client at initialization.
const Instructions = `LeoDraw MCP controls the CorelDRAW application running on this Windows PC.

Conventions used by every tool:
- Lengths are millimetres, font sizes points, angles degrees.
- Coordinates are relative to the page: origin at the TOP-LEFT corner of the page, X to the right, Y DOWN (like SVG). A shape's x/y is the top-left corner of its bounding box.
- Shapes are referenced by numeric "id" (CorelDRAW StaticID) as returned by list_shapes and creation tools.
- Colours: "#RRGGBB", "rgb(r,g,b)", "cmyk(c,m,y,k)", CSS colour names ("red", "gold") or "none".
- Every modifying tool call is exactly one undo step in CorelDRAW; use the undo tool to revert it.
- Tools working on existing shapes take "ids" (list) or "selection": true. Positions can use "anchor" (top-left, center, …).
- Angles: degrees, positive = counter-clockwise (CorelDRAW convention). Font sizes in points.

Workflow: call corel_status first. Use list_shapes (compact: true for big pages) or find_shapes (also by region) to understand the document and render_preview to look at the page or a zoomed region – always check your drawing visually with render_preview after making changes.
Speed: for many small edits use batch (several tool calls in one request, one undo step). Copy shapes between pages with copy_shapes. Templates with {{placeholders}}: fill_template.`

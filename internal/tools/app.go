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
	"time"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type noArgs struct{}

type stepsArgs struct {
	Steps int `json:"steps"`
}

type docInfo struct {
	Index    int    `json:"index"`
	Title    string `json:"title"`
	Path     string `json:"path,omitempty"`
	Pages    int64  `json:"pages"`
	Modified bool   `json:"modified"`
	Active   bool   `json:"active,omitempty"`
}

type activeInfo struct {
	Document       string    `json:"document"`
	Page           int       `json:"page"`
	PageSizeMM     geom.Rect `json:"page_size_mm"`
	Layer          string    `json:"layer"`
	SelectionCount int64     `json:"selection_count"`
}

type statusResult struct {
	Connected bool              `json:"connected"`
	Message   string            `json:"message,omitempty"`
	App       *corel.Info       `json:"app,omitempty"`
	Documents []docInfo         `json:"documents"`
	Active    *activeInfo       `json:"active,omitempty"`
	About     map[string]string `json:"about"`
}

func appTools(d *Deps) []*mcp.Tool {
	tools := []*mcp.Tool{
		{
			Name:    "corel_status",
			Toolset: "app",
			Description: "Check the connection to CorelDRAW and get an overview: version, open documents, " +
				"the active document, page (size in mm), layer and number of selected shapes. Call this first.",
			Annotations: readOnly("CorelDRAW status"),
			Handler: handler(defaultTimeout, func(ctx context.Context, _ *noArgs) (*mcp.Result, error) {
				return status(ctx, d)
			}),
		},
		{
			Name:        "corel_launch",
			Toolset:     "app",
			Description: "Start CorelDRAW (if it is not running) and connect to it. Starting can take a while.",
			Annotations: modifying("Launch CorelDRAW"),
			Handler: handler(3*defaultTimeout, func(ctx context.Context, _ *noArgs) (*mcp.Result, error) {
				info, err := d.S.Launch(ctx)
				if err != nil {
					return nil, err
				}
				return mcp.JSONResult("CorelDRAW is running.", info), nil
			}),
		},
		{
			Name:    "undo",
			Toolset: "app",
			Description: "Undo the last N steps in the active CorelDRAW document. Each modifying LeoDraw tool call " +
				"is one step; user actions in CorelDRAW are steps too.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"steps": mcp.Default(mcp.Range(mcp.Integer("Number of steps to undo"), 1, 100), 1),
			}),
			Annotations: modifying("Undo"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *stepsArgs) (*mcp.Result, error) {
				return undoRedo(ctx, d, "Undo", a.Steps)
			}),
		},
		{
			Name:        "redo",
			Toolset:     "app",
			Description: "Redo the last N undone steps in the active CorelDRAW document.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"steps": mcp.Default(mcp.Range(mcp.Integer("Number of steps to redo"), 1, 100), 1),
			}),
			Annotations: modifying("Redo"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *stepsArgs) (*mcp.Result, error) {
				return undoRedo(ctx, d, "Redo", a.Steps)
			}),
		},
	}
	if d.Supervised {
		tools = append(tools, &mcp.Tool{
			Name:    "server_reload",
			Toolset: "app",
			Description: "Restart the LeoDraw server process so that an updated leodraw-mcp.exe on disk is loaded, " +
				"without restarting the MCP client. The tool list is refreshed automatically; wait a few seconds before the next call.",
			Annotations: modifying("Reload LeoDraw server"),
			Handler: handler(defaultTimeout, func(ctx context.Context, _ *noArgs) (*mcp.Result, error) {
				go func() {
					time.Sleep(300 * time.Millisecond) // let the response reach the client first
					os.Exit(ExitReload)
				}()
				return mcp.TextResult("LeoDraw server is restarting – the updated version will be active in a few seconds."), nil
			}),
		})
	}
	return tools
}

// ExitReload is the exit code that asks the supervisor to start the server again.
const ExitReload = 3

func status(ctx context.Context, d *Deps) (*mcp.Result, error) {
	res := statusResult{Documents: []docInfo{}, About: About}
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		info := c.Info()
		res.Connected, res.App = true, &info
		docs, err := c.App.GetObj("Documents")
		if err != nil {
			return err
		}
		items, _, err := corel.Collect(docs, 50)
		if err != nil {
			return err
		}
		active, err := c.App.GetObjOrNil("ActiveDocument")
		if err != nil {
			return err
		}
		activeKey := ""
		if active != nil {
			activeKey = docKey(active)
		}
		for i, doc := range items {
			di := docInfo{Index: i + 1}
			di.Title, _ = doc.GetString("Title")
			di.Path, _ = doc.GetString("FullFileName")
			if pages, err := doc.GetObj("Pages"); err == nil {
				di.Pages, _ = pages.GetInt("Count")
			}
			di.Modified, _ = doc.GetBool("Dirty")
			di.Active = active != nil && docKey(doc) == activeKey
			res.Documents = append(res.Documents, di)
		}
		if active == nil {
			return nil
		}
		ai := &activeInfo{}
		ai.Document, _ = active.GetString("Title")
		err = c.WithMM(active, func() error {
			page, idx, err := c.Page(active, 0)
			if err != nil {
				return err
			}
			ai.Page = idx
			f, err := c.Frame(page)
			if err != nil {
				return err
			}
			ai.PageSizeMM = geom.Rect{W: geom.Round(f.Width), H: geom.Round(f.Height)}
			return nil
		})
		if err != nil {
			return err
		}
		if l, err := active.GetObj("ActiveLayer"); err == nil {
			ai.Layer, _ = l.GetString("Name")
		}
		if sel, err := active.GetObj("SelectionRange"); err == nil {
			ai.SelectionCount, _ = sel.GetInt("Count")
		}
		res.Active = ai
		return nil
	})
	if errors.Is(err, corel.ErrNotRunning) {
		res.Connected = false
		res.Message = err.Error()
		return mcp.JSONResult("", res), nil
	}
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult("", res), nil
}

func docKey(doc *com.Dispatch) string {
	t, _ := doc.GetString("Title")
	p, _ := doc.GetString("FullFileName")
	return t + "\x00" + p
}

func undoRedo(ctx context.Context, d *Deps, method string, steps int) (*mcp.Result, error) {
	if steps <= 0 {
		steps = 1
	}
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		_, err = doc.Call(method, steps)
		_, _ = c.App.Call("Refresh")
		return err
	})
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(fmt.Sprintf("%s: %d step(s) done.", method, steps)), nil
}

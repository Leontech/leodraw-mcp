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

type layerInfo struct {
	Name      string `json:"name"`
	Visible   bool   `json:"visible"`
	Printable bool   `json:"printable"`
	Editable  bool   `json:"editable"`
	Master    bool   `json:"master,omitempty"`
	Shapes    int64  `json:"shapes"`
	Active    bool   `json:"active,omitempty"`
}

type listLayersArgs struct {
	Page    int  `json:"page"`
	Special bool `json:"include_special"`
}

type layerArgs struct {
	Name     string `json:"name"`
	Page     int    `json:"page"`
	Activate bool   `json:"activate"`
}

type deleteLayerArgs struct {
	Name  string `json:"name"`
	Page  int    `json:"page"`
	Force bool   `json:"force"`
}

type setLayerArgs struct {
	Name      string  `json:"name"`
	Page      int     `json:"page"`
	NewName   *string `json:"new_name"`
	Visible   *bool   `json:"visible"`
	Printable *bool   `json:"printable"`
	Editable  *bool   `json:"editable"`
	Active    bool    `json:"active"`
}

type moveToLayerArgs struct {
	targetArgs
	Layer      string `json:"layer"`
	TargetPage int    `json:"target_page"`
}

func layerTools(d *Deps) []*mcp.Tool {
	nameProp := func(p map[string]mcp.Schema) map[string]mcp.Schema {
		p["name"] = mcp.String("Layer name.")
		p["page"] = mcp.Integer("Page number. Default: the active page.")
		return p
	}
	return []*mcp.Tool{
		{
			Name:        "list_layers",
			Toolset:     "layers",
			Description: "List the layers of a page (top to bottom, incl. master layers): name, visible, printable, editable, master, shape count, active.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"page":            mcp.Integer("Page number. Default: the active page."),
				"include_special": mcp.Boolean("Include the special Guides / Desktop / Grid layers."),
			}),
			Annotations: readOnly("List layers"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *listLayersArgs) (*mcp.Result, error) {
				return listLayers(ctx, d, a)
			}),
		},
		{
			Name:        "create_layer",
			Toolset:     "layers",
			Description: "Create a new layer on a page (on top). activate: make it the layer new shapes are drawn on.",
			InputSchema: mcp.Object([]string{"name"}, nameProp(map[string]mcp.Schema{
				"activate": mcp.Boolean("Make it the active layer."),
			})),
			Annotations: modifying("Create layer"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *layerArgs) (*mcp.Result, error) {
				return createLayer(ctx, d, a)
			}),
		},
		{
			Name:        "delete_layer",
			Toolset:     "layers",
			Description: "Delete a layer. A layer that contains shapes is deleted only with force: true (its shapes are deleted too).",
			InputSchema: mcp.Object([]string{"name"}, nameProp(map[string]mcp.Schema{
				"force": mcp.Boolean("Delete even if the layer contains shapes."),
			})),
			Annotations: destructive("Delete layer"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *deleteLayerArgs) (*mcp.Result, error) {
				return deleteLayer(ctx, d, a)
			}),
		},
		{
			Name:        "set_layer",
			Toolset:     "layers",
			Description: "Change a layer: rename, show/hide, printable, editable (lock), make active.",
			InputSchema: mcp.Object([]string{"name"}, nameProp(map[string]mcp.Schema{
				"new_name":  mcp.String("New name."),
				"visible":   mcp.Boolean("Visible."),
				"printable": mcp.Boolean("Printable / exportable."),
				"editable":  mcp.Boolean("Editable (false = locked)."),
				"active":    mcp.Boolean("Make it the active layer."),
			})),
			Annotations: modifying("Set layer"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *setLayerArgs) (*mcp.Result, error) {
				return setLayer(ctx, d, a)
			}),
		},
		{
			Name:        "set_active_layer",
			Toolset:     "layers",
			Description: "Make a layer the active one – shapes created without a layer argument are drawn on it.",
			InputSchema: mcp.Object([]string{"name"}, nameProp(map[string]mcp.Schema{})),
			Annotations: modifying("Set active layer"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *layerArgs) (*mcp.Result, error) {
				return setLayer(ctx, d, &setLayerArgs{Name: a.Name, Page: a.Page, Active: true})
			}),
		},
		{
			Name:        "move_to_layer",
			Toolset:     "layers",
			Description: "Move shapes (ids or selection) to another layer – on the same page or, with target_page, on another page.",
			InputSchema: mcp.Object([]string{"layer"}, targetSchema(map[string]mcp.Schema{
				"layer":       mcp.String("Target layer name."),
				"target_page": mcp.Integer("Page of the target layer. Default: the shapes' page."),
			})),
			Annotations: modifying("Move to layer"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *moveToLayerArgs) (*mcp.Result, error) {
				return moveToLayer(ctx, d, a)
			}),
		},
	}
}

func describeLayer(l *com.Dispatch, activeName string) layerInfo {
	var li layerInfo
	li.Name, _ = l.GetString("Name")
	li.Visible, _ = l.GetBool("Visible")
	li.Printable, _ = l.GetBool("Printable")
	li.Editable, _ = l.GetBool("Editable")
	li.Master, _ = l.GetBool("Master")
	if sh, err := l.GetObj("Shapes"); err == nil {
		li.Shapes, _ = sh.GetInt("Count")
	}
	li.Active = li.Name == activeName
	return li
}

func activeLayerName(doc *com.Dispatch) string {
	if l, err := doc.GetObj("ActiveLayer"); err == nil {
		n, _ := l.GetString("Name")
		return n
	}
	return ""
}

func listLayers(ctx context.Context, d *Deps, a *listLayersArgs) (*mcp.Result, error) {
	var out []layerInfo
	page := 0
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		p, idx, err := c.Page(doc, a.Page)
		if err != nil {
			return err
		}
		page = idx
		layers, err := p.GetObj("Layers")
		if err != nil {
			return err
		}
		list, _, err := corel.Collect(layers, 0)
		if err != nil {
			return err
		}
		active := activeLayerName(doc)
		for _, l := range list { // Item(1) is the top layer
			if !a.Special {
				if sp, _ := l.GetBool("IsSpecialLayer"); sp {
					continue
				}
			}
			out = append(out, describeLayer(l, active))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Page %d has %d layer(s) (top first).", page, len(out)), map[string]any{"page": page, "layers": out}), nil
}

func createLayer(ctx context.Context, d *Deps, a *layerArgs) (*mcp.Result, error) {
	if a.Name == "" {
		return nil, errors.New("name must not be empty")
	}
	var li layerInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		p, _, err := c.Page(doc, a.Page)
		if err != nil {
			return err
		}
		if _, err := c.Layer(doc, p, a.Name); err == nil {
			return fmt.Errorf("layer %q already exists on this page", a.Name)
		}
		return c.Write(doc, "create_layer", func() error {
			l, err := p.CallObj("CreateLayer", a.Name)
			if err != nil {
				return err
			}
			if a.Activate {
				if err := activateLayer(doc, l); err != nil {
					return err
				}
			}
			li = describeLayer(l, activeLayerName(doc))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Layer %q created.", li.Name), li), nil
}

func deleteLayer(ctx context.Context, d *Deps, a *deleteLayerArgs) (*mcp.Result, error) {
	if a.Name == "" {
		return nil, errors.New("name must not be empty")
	}
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		p, _, err := c.Page(doc, a.Page)
		if err != nil {
			return err
		}
		l, err := c.Layer(doc, p, a.Name)
		if err != nil {
			return err
		}
		if sh, err := l.GetObj("Shapes"); err == nil {
			if n, _ := sh.GetInt("Count"); n > 0 && !a.Force {
				return fmt.Errorf("layer %q contains %d shape(s) – use force: true to delete them with the layer", a.Name, n)
			}
		}
		return c.Write(doc, "delete_layer", func() error {
			_, err := l.Call("Delete")
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(fmt.Sprintf("Layer %q deleted.", a.Name)), nil
}

func setLayer(ctx context.Context, d *Deps, a *setLayerArgs) (*mcp.Result, error) {
	if a.Name == "" {
		return nil, errors.New("name must not be empty")
	}
	var li layerInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		p, idx, err := c.Page(doc, a.Page)
		if err != nil {
			return err
		}
		l, err := c.Layer(doc, p, a.Name)
		if err != nil {
			return err
		}
		return c.Write(doc, "set_layer", func() error {
			for _, pr := range []struct {
				n string
				v any
			}{{"Name", a.NewName}, {"Visible", a.Visible}, {"Printable", a.Printable}, {"Editable", a.Editable}} {
				switch v := pr.v.(type) {
				case *string:
					if v != nil {
						if *v == "" {
							return errors.New("new_name must not be empty")
						}
						if err := l.Put(pr.n, *v); err != nil {
							return err
						}
					}
				case *bool:
					if v != nil {
						if err := l.Put(pr.n, *v); err != nil {
							return err
						}
					}
				}
			}
			if a.Active {
				if _, active, _ := c.Page(doc, 0); active != idx {
					if _, err := p.Call("Activate"); err != nil {
						return err
					}
				}
				if err := activateLayer(doc, l); err != nil {
					return err
				}
			}
			li = describeLayer(l, activeLayerName(doc))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Layer %q updated.", li.Name), li), nil
}

func moveToLayer(ctx context.Context, d *Deps, a *moveToLayerArgs) (*mcp.Result, error) {
	n := 0
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "move_to_layer", func(p *pageCtx) error {
			_, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			tp := p.page
			if a.TargetPage != 0 {
				if tp, _, err = c.Page(p.doc, a.TargetPage); err != nil {
					return err
				}
			}
			l, err := c.Layer(p.doc, tp, a.Layer)
			if err != nil {
				return err
			}
			for _, s := range list {
				if _, err := s.Call("MoveToLayer", l); err != nil {
					return err
				}
			}
			n = len(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(fmt.Sprintf("Moved %d shape(s) to layer %q.", n, a.Layer)), nil
}

// activateLayer makes l the active layer. CorelDRAW makes the layer of the
// selected shapes active, so the selection is cleared first.
func activateLayer(doc, l *com.Dispatch) error {
	if _, err := doc.Call("ClearSelection"); err != nil {
		return err
	}
	_, err := l.Call("Activate")
	return err
}

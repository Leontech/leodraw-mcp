//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

type describeArgs struct {
	Path   string `json:"path"`
	Filter string `json:"filter"`
	Enum   string `json:"enum"`
	Type   string `json:"type"`
}

func describeTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{{
		Name:    "api_describe",
		Toolset: "inspect",
		Description: "Developer aid: list methods and properties of a CorelDRAW automation object with their exact " +
			"signatures, read from the type library of the installed CorelDRAW version. path is a dotted property path " +
			`from the Application object, e.g. "" (Application), "ActiveDocument", "ActiveDocument.ActivePage", ` +
			`"ActiveDocument.ActiveLayer", "ActiveSelection". filter = substring of member names (e.g. "Export"). ` +
			`With enum (e.g. "cdrFilter" or several comma-separated) it lists the values of those enumerations; ` +
			`with type (e.g. "IVGTextRange", "IVGCurve") it describes an interface by name, no object needed.`,
		InputSchema: mcp.Object(nil, map[string]mcp.Schema{
			"path":   mcp.String(`Dotted property path from Application. Empty = Application.`),
			"filter": mcp.String("Only members (or enum constants) whose name contains this text (case-insensitive)."),
			"enum":   mcp.String(`List the constants of these enumerations (comma-separated), e.g. "cdrFilter,cdrAlignType".`),
			"type":   mcp.String(`Describe this interface by name, e.g. "IVGTextRange".`),
		}),
		Annotations: readOnly("Describe CorelDRAW API"),
		Handler: handler(defaultTimeout, func(ctx context.Context, a *describeArgs) (*mcp.Result, error) {
			if a.Enum != "" {
				var b strings.Builder
				err := d.S.Run(ctx, func(c *corel.Ctx) error {
					for _, name := range strings.Split(a.Enum, ",") {
						name = strings.TrimSpace(name)
						consts, err := c.App.Enum(name, a.Filter)
						if err != nil {
							fmt.Fprintf(&b, "%s: %v\n\n", name, err)
							continue
						}
						fmt.Fprintf(&b, "%s – %d constant(s)\n", name, len(consts))
						for _, k := range consts {
							fmt.Fprintf(&b, "  %s = %d\n", k.Name, k.Value)
						}
						b.WriteString("\n")
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				return mcp.TextResult(b.String()), nil
			}
			var typeName string
			var members []com.Member
			err := d.S.Run(ctx, func(c *corel.Ctx) error {
				obj := c.App
				for _, seg := range strings.Split(strings.Trim(a.Path, ". "), ".") {
					if seg == "" || strings.EqualFold(seg, "Application") {
						continue
					}
					next, err := obj.GetObj(seg)
					if err != nil {
						return fmt.Errorf("path %q: %w", a.Path, err)
					}
					obj = next
				}
				var err error
				if a.Type != "" {
					typeName, members, err = obj.DescribeType(a.Type, a.Filter)
				} else {
					typeName, members, err = obj.Describe(a.Filter)
				}
				return err
			})
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s – %d member(s)\n", typeName, len(members))
			for _, m := range members {
				fmt.Fprintf(&b, "%-6s %s\n", m.Kind, m.Signature)
			}
			return mcp.TextResult(b.String()), nil
		}),
	}}
}

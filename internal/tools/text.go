//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/colors"
	"github.com/Leontech/leodraw-mcp/internal/com"
	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/geom"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
)

// text constants (VGCore)
const (
	cdrLanguageMixed  = -1
	cdrCharSetMixed   = -1
	cdrUndefined      = -2
	cdrMixedFontLine  = 7
	cdrNoFontLine     = 0
	cdrSingleThinLine = 1
)

var alignments = map[string]int{"left": 1, "right": 2, "center": 3, "justify": 4, "force_justify": 5}
var alignmentNames = map[int64]string{0: "none", 1: "left", 2: "right", 3: "center", 4: "justify", 5: "force_justify", 6: "mixed"}

type fontArgs struct {
	Font      string   `json:"font"`
	Size      *float64 `json:"size"`
	Bold      *bool    `json:"bold"`
	Italic    *bool    `json:"italic"`
	Underline *bool    `json:"underline"`
	Align     string   `json:"align"`
}

func fontSchema(p map[string]mcp.Schema) map[string]mcp.Schema {
	p["font"] = mcp.String(`Font family, e.g. "Arial" (see list_fonts). Default: CorelDRAW default font.`)
	p["size"] = mcp.Number("Font size in points.")
	p["bold"] = mcp.Boolean("Bold.")
	p["italic"] = mcp.Boolean("Italic.")
	p["underline"] = mcp.Boolean("Underline.")
	p["align"] = mcp.Enum("Text alignment.", "left", "center", "right", "justify", "force_justify")
	return p
}

func triState(b *bool) int {
	if b == nil {
		return cdrUndefined
	}
	return cdrBool(*b)
}

func (a *fontArgs) check() error {
	if a.Align != "" {
		if _, ok := alignments[a.Align]; !ok {
			return fmt.Errorf("unknown align %q", a.Align)
		}
	}
	if a.Size != nil && (*a.Size <= 0 || *a.Size > 3000) {
		return errors.New("size must be 0 – 3000 pt")
	}
	return nil
}

// createParams returns the optional parameters of CreateArtisticText / CreateParagraphText
// (LanguageID, CharSet, Font, Size, Bold, Italic, Underline, Alignment) – all must be passed.
func (a *fontArgs) createParams() []any {
	size := 0.0
	if a.Size != nil {
		size = *a.Size
	}
	ul := cdrMixedFontLine
	if a.Underline != nil {
		ul = cdrNoFontLine
		if *a.Underline {
			ul = cdrSingleThinLine
		}
	}
	al := 1
	if v, ok := alignments[a.Align]; ok {
		al = v
	}
	return []any{cdrLanguageMixed, cdrCharSetMixed, a.Font, float32(size), triState(a.Bold), triState(a.Italic), ul, al}
}

type textArgs struct {
	Text   string  `json:"text"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Anchor string  `json:"anchor"`
	Box    string  `json:"box"`
	fontArgs
	styleArgs
}

type paraTextArgs struct {
	Text   string  `json:"text"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	fontArgs
	styleArgs
}

type setTextArgs struct {
	targetArgs
	Text string `json:"text"`
}

type textStyleArgs struct {
	targetArgs
	Start       *int     `json:"start"`
	Length      *int     `json:"length"`
	Color       string   `json:"color"`
	LineSpacing *float64 `json:"line_spacing"`
	CharSpacing *float64 `json:"char_spacing"`
	fontArgs
}

type replaceTextArgs struct {
	Find          string  `json:"find"`
	Replace       string  `json:"replace"`
	CaseSensitive bool    `json:"case_sensitive"`
	Page          int     `json:"page"`
	AllPages      bool    `json:"all_pages"`
	IDs           []int64 `json:"ids"`
}

type listFontsArgs struct {
	Filter string `json:"filter"`
	Limit  int    `json:"limit"`
}

func textTools(d *Deps) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name:    "create_text",
			Toolset: "text",
			Description: "Create artistic text (a single or multi-line label; \\n = new line). x, y position the text's bounding box " +
				"by its anchor (default top-left; use anchor \"center\" to centre a label on a point). fill = text colour.",
			InputSchema: mcp.Object([]string{"text", "x", "y"}, fontSchema(styleSchema(map[string]mcp.Schema{
				"text":   mcp.String("The text."),
				"x":      mcp.Number("x of the anchor point in mm."),
				"y":      mcp.Number("y of the anchor point in mm."),
				"anchor": anchorSchema(),
				"box": mcp.Enum("What the anchor refers to: \"glyphs\" (default, the ink of the characters) or \"line\" "+
					"(the text line box – font ascent to descent; use it to centre short texts like \"-\" or \"--\" in a table row "+
					"the same way as other texts of the same size).", "glyphs", "line"),
			}))),
			Annotations: modifying("Create text"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *textArgs) (*mcp.Result, error) {
				return createText(ctx, d, a)
			}),
		},
		{
			Name:    "create_paragraph_text",
			Toolset: "text",
			Description: "Create paragraph text that wraps inside a frame (x, y = top-left, width, height in mm). " +
				"Use for longer text; the result reports overflow when the text does not fit.",
			InputSchema: mcp.Object([]string{"text", "x", "y", "width", "height"}, fontSchema(styleSchema(map[string]mcp.Schema{
				"text":   mcp.String("The text (\\n = new paragraph)."),
				"x":      mcp.Number("Left edge of the frame in mm."),
				"y":      mcp.Number("Top edge of the frame in mm."),
				"width":  mcp.Number("Frame width in mm."),
				"height": mcp.Number("Frame height in mm."),
			}))),
			Annotations: modifying("Create paragraph text"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *paraTextArgs) (*mcp.Result, error) {
				return createParaText(ctx, d, a)
			}),
		},
		{
			Name:        "set_text",
			Toolset:     "text",
			Description: "Replace the whole content of text shapes (ids or selection). Character formatting of the first character is kept.",
			InputSchema: mcp.Object([]string{"text"}, targetSchema(map[string]mcp.Schema{
				"text": mcp.String("New text (\\n = new line)."),
			})),
			Annotations: modifying("Set text"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *setTextArgs) (*mcp.Result, error) {
				return setText(ctx, d, a)
			}),
		},
		{
			Name:    "set_text_style",
			Toolset: "text",
			Description: "Change the formatting of text shapes (ids or selection): font, size (pt), bold, italic, underline, alignment, colour, " +
				"line spacing and character spacing. start/length limit the change to a range of characters (0-based).",
			InputSchema: mcp.Object(nil, fontSchema(targetSchema(map[string]mcp.Schema{
				"start":        mcp.Integer("First character (0-based) of the range to format. Default: whole text."),
				"length":       mcp.Integer("Number of characters of the range."),
				"color":        mcp.String(`Text colour ("#RRGGBB", "cmyk(…)", name).`),
				"line_spacing": mcp.Number("Line spacing in % of character height (100 = single)."),
				"char_spacing": mcp.Number("Character spacing in % of a space (0 = normal)."),
			}))),
			Annotations: modifying("Set text style"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *textStyleArgs) (*mcp.Result, error) {
				return setTextStyle(ctx, d, a)
			}),
		},
		{
			Name:    "replace_text",
			Toolset: "text",
			Description: "Find and replace text in all text shapes of a page or the whole document (e.g. fill placeholders like {{name}}). " +
				"Formatting is kept. Returns how many occurrences were found.",
			InputSchema: mcp.Object([]string{"find", "replace"}, map[string]mcp.Schema{
				"find":           mcp.String("Text to find."),
				"replace":        mcp.String("Replacement text."),
				"case_sensitive": mcp.Boolean("Match case (default false)."),
				"page":           mcp.Integer("Page number. Default: the active page."),
				"all_pages":      mcp.Boolean("Replace on all pages."),
				"ids":            mcp.Array("Only in these text shapes (on page).", mcp.Integer("id")),
			}),
			Annotations: modifying("Replace text"),
			Handler: handler(2*defaultTimeout, func(ctx context.Context, a *replaceTextArgs) (*mcp.Result, error) {
				return replaceText(ctx, d, a)
			}),
		},
		{
			Name:        "list_fonts",
			Toolset:     "inspect",
			Description: "List font families installed on this PC (as CorelDRAW sees them), optionally filtered by a substring.",
			InputSchema: mcp.Object(nil, map[string]mcp.Schema{
				"filter": mcp.String(`Only fonts containing this text (case-insensitive), e.g. "sans".`),
				"limit":  mcp.Default(mcp.Range(mcp.Integer("Maximum number of fonts."), 1, 5000), 300),
			}),
			Annotations: readOnly("List fonts"),
			Handler: handler(defaultTimeout, func(ctx context.Context, a *listFontsArgs) (*mcp.Result, error) {
				return listFonts(ctx, d, a)
			}),
		},
	}
}

func crText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, `\n`, "\n") // a literal backslash-n from the client
	return strings.ReplaceAll(s, "\n", "\r")
}

func createText(ctx context.Context, d *Deps, a *textArgs) (*mcp.Result, error) {
	if a.Text == "" {
		return nil, errors.New("text must not be empty")
	}
	if err := a.fontArgs.check(); err != nil {
		return nil, err
	}
	if _, err := anchorFrac(a.Anchor); err != nil {
		return nil, err
	}
	if a.Box != "" && a.Box != "glyphs" && a.Box != "line" {
		return nil, fmt.Errorf("unknown box %q", a.Box)
	}
	return createAndStyle(ctx, d, &a.styleArgs, "create_text", "Text", func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error) {
		x, y := f.FromPage(a.X, a.Y)
		args := append([]any{x, y, crText(a.Text)}, a.fontArgs.createParams()...)
		s, err := layer.CallObj("CreateArtisticText", args...)
		if err != nil {
			return nil, err
		}
		if a.Box == "line" {
			return s, placeByLineBox(layer, s, f, a.X, a.Y, a.Anchor)
		}
		return s, placeAt(s, f, a.X, a.Y, a.Anchor)
	})
}

func createParaText(ctx context.Context, d *Deps, a *paraTextArgs) (*mcp.Result, error) {
	if a.Width <= 0 || a.Height <= 0 {
		return nil, errors.New("width and height must be greater than 0")
	}
	if err := a.fontArgs.check(); err != nil {
		return nil, err
	}
	res, err := createAndStyle(ctx, d, &a.styleArgs, "create_paragraph_text", "Paragraph text", func(c *corel.Ctx, doc, layer *com.Dispatch, f geom.Frame) (*com.Dispatch, error) {
		l, t := f.FromPage(a.X, a.Y)
		r, b := f.FromPage(a.X+a.Width, a.Y+a.Height)
		args := append([]any{l, t, r, b, crText(a.Text)}, a.fontArgs.createParams()...)
		return layer.CallObj("CreateParagraphText", args...)
	})
	return res, err
}

// story returns the TextRange of the whole text of a text shape.
func story(s *com.Dispatch) (*com.Dispatch, *com.Dispatch, error) {
	if t, _ := s.GetInt("Type"); t != 6 {
		id, _ := s.GetInt("StaticID")
		return nil, nil, fmt.Errorf("shape %d is not a text shape", id)
	}
	txt, err := s.GetObj("Text")
	if err != nil {
		return nil, nil, err
	}
	st, err := txt.GetObj("Story")
	return txt, st, err
}

func setText(ctx context.Context, d *Deps, a *setTextArgs) (*mcp.Result, error) {
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "set_text", func(p *pageCtx) error {
			_, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			for _, s := range list {
				_, st, err := story(s)
				if err != nil {
					return err
				}
				if err := st.Put("Text", crText(a.Text)); err != nil {
					return err
				}
			}
			infos = p.describe(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Text set on %d shape(s).", len(infos)), map[string]any{"shapes": infos}), nil
}

func setTextStyle(ctx context.Context, d *Deps, a *textStyleArgs) (*mcp.Result, error) {
	if err := a.fontArgs.check(); err != nil {
		return nil, err
	}
	var col *colors.Color
	if a.Color != "" {
		cc, err := colors.Parse(a.Color)
		if err != nil {
			return nil, err
		}
		col = &cc
	}
	var infos []corel.ShapeInfo
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		return withPage(c, a.Page, "set_text_style", func(p *pageCtx) error {
			_, list, err := p.shapes(&a.targetArgs)
			if err != nil {
				return err
			}
			for _, s := range list {
				txt, rng, err := story(s)
				if err != nil {
					return err
				}
				if a.Start != nil || a.Length != nil {
					start := 0
					if a.Start != nil {
						start = *a.Start
					}
					n, _ := rng.GetInt("Length")
					end := int(n)
					if a.Length != nil {
						end = start + *a.Length
					}
					if start < 0 || end > int(n) || start >= end {
						return fmt.Errorf("range %d–%d is outside the text (length %d)", start, end, n)
					}
					if rng, err = txt.CallObj("Range", start, end); err != nil {
						return err
					}
				}
				set := func(name string, v any) error {
					if err := rng.Put(name, v); err != nil {
						return fmt.Errorf("setting %s: %w", name, err)
					}
					return nil
				}
				if a.Font != "" {
					if err := set("Font", a.Font); err != nil {
						return err
					}
				}
				if a.Size != nil {
					if err := set("Size", float32(*a.Size)); err != nil {
						return err
					}
				}
				if a.Bold != nil {
					if err := set("Bold", *a.Bold); err != nil {
						return err
					}
				}
				if a.Italic != nil {
					if err := set("Italic", *a.Italic); err != nil {
						return err
					}
				}
				if a.Underline != nil {
					ul := cdrNoFontLine
					if *a.Underline {
						ul = cdrSingleThinLine
					}
					if err := set("Underline", ul); err != nil {
						return err
					}
				}
				if a.Align != "" {
					if err := set("Alignment", alignments[a.Align]); err != nil {
						return err
					}
				}
				if a.LineSpacing != nil {
					if err := set("LineSpacing", float32(*a.LineSpacing)); err != nil {
						return err
					}
				}
				if a.CharSpacing != nil {
					if err := set("CharSpacing", float32(*a.CharSpacing)); err != nil {
						return err
					}
				}
				if col != nil {
					fill, err := rng.GetObj("Fill")
					if err != nil {
						return err
					}
					cc, err := c.NewColor(*col)
					if err != nil {
						return err
					}
					if _, err := fill.Call("ApplyUniformFill", cc); err != nil {
						return err
					}
				}
			}
			infos = p.describe(list)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Text style changed on %d shape(s).", len(infos)), map[string]any{"shapes": infos}), nil
}

func replaceText(ctx context.Context, d *Deps, a *replaceTextArgs) (*mcp.Result, error) {
	if a.Find == "" {
		return nil, errors.New("find must not be empty")
	}
	type pageCount struct {
		Page  int `json:"page"`
		Count int `json:"count"`
	}
	var counts []pageCount
	total := 0
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		doc, err := c.ActiveDocument()
		if err != nil {
			return err
		}
		var pages []*com.Dispatch
		if a.AllPages {
			ps, err := doc.GetObj("Pages")
			if err != nil {
				return err
			}
			if pages, _, err = corel.Collect(ps, 0); err != nil {
				return err
			}
		} else {
			p, _, err := c.Page(doc, a.Page)
			if err != nil {
				return err
			}
			pages = []*com.Dispatch{p}
		}
		if len(a.IDs) > 0 {
			if a.AllPages {
				return errors.New("ids cannot be combined with all_pages")
			}
			return c.Write(doc, "replace_text", func() error {
				find := a.Find
				if !a.CaseSensitive {
					find = strings.ToLower(find)
				}
				n := 0
				for _, id := range a.IDs {
					s, err := corel.FindShape(pages[0], id)
					if err != nil {
						return err
					}
					txt, st, err := story(s)
					if err != nil {
						return err
					}
					content, _ := st.GetString("Text")
					cmp := content
					if !a.CaseSensitive {
						cmp = strings.ToLower(cmp)
					}
					// replace from the end so that earlier positions stay valid
					var pos []int
					for i := 0; ; {
						j := strings.Index(cmp[i:], find)
						if j < 0 {
							break
						}
						pos = append(pos, i+j)
						i += j + len(find)
					}
					for k := len(pos) - 1; k >= 0; k-- {
						start := len([]rune(content[:pos[k]]))
						end := start + len([]rune(content[pos[k]:pos[k]+len(find)]))
						r, err := txt.CallObj("Range", start, end)
						if err != nil {
							return err
						}
						if err := r.Put("Text", crText(a.Replace)); err != nil {
							return err
						}
						n++
					}
				}
				idx, _ := pages[0].GetInt("Index")
				counts = append(counts, pageCount{int(idx), n})
				total = n
				return nil
			})
		}
		return c.Write(doc, "replace_text", func() error {
			for _, pg := range pages {
				idx, _ := pg.GetInt("Index")
				n := countText(pg, a.Find, a.CaseSensitive)
				if n == 0 {
					continue
				}
				if _, err := pg.Call("TextReplace", a.Find, a.Replace, a.CaseSensitive, false); err != nil {
					return err
				}
				counts = append(counts, pageCount{int(idx), n})
				total += n
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("Replaced %d occurrence(s) of %q.", total, a.Find), map[string]any{"total": total, "pages": counts}), nil
}

// countText counts occurrences of find in all text shapes of a page.
func countText(page *com.Dispatch, find string, caseSensitive bool) int {
	layers, err := page.GetObj("Layers")
	if err != nil {
		return 0
	}
	ls, _, _ := corel.Collect(layers, 0)
	if !caseSensitive {
		find = strings.ToLower(find)
	}
	n := 0
	for _, l := range ls {
		shapes, err := l.GetObj("Shapes")
		if err != nil {
			continue
		}
		walkShapes(shapes, 0, 20, func(s *com.Dispatch, _ int) bool {
			if _, st, err := story(s); err == nil {
				t, _ := st.GetString("Text")
				if !caseSensitive {
					t = strings.ToLower(t)
				}
				n += strings.Count(t, find)
			}
			return true
		})
	}
	return n
}

func listFonts(ctx context.Context, d *Deps, a *listFontsArgs) (*mcp.Result, error) {
	if a.Limit <= 0 {
		a.Limit = 300
	}
	var fonts []string
	total := 0
	err := d.S.Run(ctx, func(c *corel.Ctx) error {
		fl, err := c.App.GetObj("FontList")
		if err != nil {
			return err
		}
		n, err := fl.GetInt("Count")
		if err != nil {
			return err
		}
		filter := strings.ToLower(a.Filter)
		for i := int64(1); i <= n; i++ {
			name, err := fl.GetString("Item", i)
			if err != nil {
				return err
			}
			if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
				continue
			}
			total++
			if len(fonts) < a.Limit {
				fonts = append(fonts, name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(fmt.Sprintf("%d font(s) found, %d listed.", total, len(fonts)),
		map[string]any{"total": total, "truncated": total > len(fonts), "fonts": fonts}), nil
}

// placeByLineBox places a text shape using its line box (TextLineRects:
// ascent to descent of every line) instead of the ink bounding box.
func placeByLineBox(layer, s *com.Dispatch, f geom.Frame, x, y float64, anchor string) error {
	_, st, err := story(s)
	if err != nil {
		return err
	}
	rects, err := st.GetObj("TextLineRects")
	if err != nil {
		return fmt.Errorf("text line box: %w", err)
	}
	tmp, err := layer.CallObj("CreateCurve", rects)
	if err != nil {
		return fmt.Errorf("text line box: %w", err)
	}
	px, py, err := anchorPoint(tmp, f, anchor)
	_, _ = tmp.Call("Delete")
	if err != nil {
		return err
	}
	return moveBy(s, f, x-px, y-py)
}

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package colors parses colour strings used by LeoDraw tools.
//
// Accepted forms: "none", "#RGB", "#RRGGBB", "rgb(r,g,b)",
// "cmyk(c,m,y,k)" (0–100) and the CSS / SVG colour names.
package colors

import (
	"fmt"
	"strconv"
	"strings"
)

// Model is the colour model.
type Model int

const (
	None Model = iota
	RGB
	CMYK
)

// Color is a parsed colour.
type Color struct {
	Model      Model
	R, G, B    int // 0–255 when Model == RGB
	C, M, Y, K int // 0–100 when Model == CMYK
}

// named are the CSS / SVG colour keywords.
var named = map[string][3]int{
	"aliceblue":            {240, 248, 255},
	"antiquewhite":         {250, 235, 215},
	"aqua":                 {0, 255, 255},
	"aquamarine":           {127, 255, 212},
	"azure":                {240, 255, 255},
	"beige":                {245, 245, 220},
	"bisque":               {255, 228, 196},
	"black":                {0, 0, 0},
	"blanchedalmond":       {255, 235, 205},
	"blue":                 {0, 0, 255},
	"blueviolet":           {138, 43, 226},
	"brown":                {165, 42, 42},
	"burlywood":            {222, 184, 135},
	"cadetblue":            {95, 158, 160},
	"chartreuse":           {127, 255, 0},
	"chocolate":            {210, 105, 30},
	"coral":                {255, 127, 80},
	"cornflowerblue":       {100, 149, 237},
	"cornsilk":             {255, 248, 220},
	"crimson":              {220, 20, 60},
	"cyan":                 {0, 255, 255},
	"darkblue":             {0, 0, 139},
	"darkcyan":             {0, 139, 139},
	"darkgoldenrod":        {184, 134, 11},
	"darkgray":             {169, 169, 169},
	"darkgreen":            {0, 100, 0},
	"darkgrey":             {169, 169, 169},
	"darkkhaki":            {189, 183, 107},
	"darkmagenta":          {139, 0, 139},
	"darkolivegreen":       {85, 107, 47},
	"darkorange":           {255, 140, 0},
	"darkorchid":           {153, 50, 204},
	"darkred":              {139, 0, 0},
	"darksalmon":           {233, 150, 122},
	"darkseagreen":         {143, 188, 143},
	"darkslateblue":        {72, 61, 139},
	"darkslategray":        {47, 79, 79},
	"darkslategrey":        {47, 79, 79},
	"darkturquoise":        {0, 206, 209},
	"darkviolet":           {148, 0, 211},
	"deeppink":             {255, 20, 147},
	"deepskyblue":          {0, 191, 255},
	"dimgray":              {105, 105, 105},
	"dimgrey":              {105, 105, 105},
	"dodgerblue":           {30, 144, 255},
	"firebrick":            {178, 34, 34},
	"floralwhite":          {255, 250, 240},
	"forestgreen":          {34, 139, 34},
	"fuchsia":              {255, 0, 255},
	"gainsboro":            {220, 220, 220},
	"ghostwhite":           {248, 248, 255},
	"gold":                 {255, 215, 0},
	"goldenrod":            {218, 165, 32},
	"gray":                 {128, 128, 128},
	"grey":                 {128, 128, 128},
	"green":                {0, 128, 0},
	"greenyellow":          {173, 255, 47},
	"honeydew":             {240, 255, 240},
	"hotpink":              {255, 105, 180},
	"indianred":            {205, 92, 92},
	"indigo":               {75, 0, 130},
	"ivory":                {255, 255, 240},
	"khaki":                {240, 230, 140},
	"lavender":             {230, 230, 250},
	"lavenderblush":        {255, 240, 245},
	"lawngreen":            {124, 252, 0},
	"lemonchiffon":         {255, 250, 205},
	"lightblue":            {173, 216, 230},
	"lightcoral":           {240, 128, 128},
	"lightcyan":            {224, 255, 255},
	"lightgoldenrodyellow": {250, 250, 210},
	"lightgray":            {211, 211, 211},
	"lightgreen":           {144, 238, 144},
	"lightgrey":            {211, 211, 211},
	"lightpink":            {255, 182, 193},
	"lightsalmon":          {255, 160, 122},
	"lightseagreen":        {32, 178, 170},
	"lightskyblue":         {135, 206, 250},
	"lightslategray":       {119, 136, 153},
	"lightslategrey":       {119, 136, 153},
	"lightsteelblue":       {176, 196, 222},
	"lightyellow":          {255, 255, 224},
	"lime":                 {0, 255, 0},
	"limegreen":            {50, 205, 50},
	"linen":                {250, 240, 230},
	"magenta":              {255, 0, 255},
	"maroon":               {128, 0, 0},
	"mediumaquamarine":     {102, 205, 170},
	"mediumblue":           {0, 0, 205},
	"mediumorchid":         {186, 85, 211},
	"mediumpurple":         {147, 112, 219},
	"mediumseagreen":       {60, 179, 113},
	"mediumslateblue":      {123, 104, 238},
	"mediumspringgreen":    {0, 250, 154},
	"mediumturquoise":      {72, 209, 204},
	"mediumvioletred":      {199, 21, 133},
	"midnightblue":         {25, 25, 112},
	"mintcream":            {245, 255, 250},
	"mistyrose":            {255, 228, 225},
	"moccasin":             {255, 228, 181},
	"navajowhite":          {255, 222, 173},
	"navy":                 {0, 0, 128},
	"oldlace":              {253, 245, 230},
	"olive":                {128, 128, 0},
	"olivedrab":            {107, 142, 35},
	"orange":               {255, 165, 0},
	"orangered":            {255, 69, 0},
	"orchid":               {218, 112, 214},
	"palegoldenrod":        {238, 232, 170},
	"palegreen":            {152, 251, 152},
	"paleturquoise":        {175, 238, 238},
	"palevioletred":        {219, 112, 147},
	"papayawhip":           {255, 239, 213},
	"peachpuff":            {255, 218, 185},
	"peru":                 {205, 133, 63},
	"pink":                 {255, 192, 203},
	"plum":                 {221, 160, 221},
	"powderblue":           {176, 224, 230},
	"purple":               {128, 0, 128},
	"rebeccapurple":        {102, 51, 153},
	"red":                  {255, 0, 0},
	"rosybrown":            {188, 143, 143},
	"royalblue":            {65, 105, 225},
	"saddlebrown":          {139, 69, 19},
	"salmon":               {250, 128, 114},
	"sandybrown":           {244, 164, 96},
	"seagreen":             {46, 139, 87},
	"seashell":             {255, 245, 238},
	"sienna":               {160, 82, 45},
	"silver":               {192, 192, 192},
	"skyblue":              {135, 206, 235},
	"slateblue":            {106, 90, 205},
	"slategray":            {112, 128, 144},
	"slategrey":            {112, 128, 144},
	"snow":                 {255, 250, 250},
	"springgreen":          {0, 255, 127},
	"steelblue":            {70, 130, 180},
	"tan":                  {210, 180, 140},
	"teal":                 {0, 128, 128},
	"thistle":              {216, 191, 216},
	"tomato":               {255, 99, 71},
	"turquoise":            {64, 224, 208},
	"violet":               {238, 130, 238},
	"wheat":                {245, 222, 179},
	"white":                {255, 255, 255},
	"whitesmoke":           {245, 245, 245},
	"yellow":               {255, 255, 0},
	"yellowgreen":          {154, 205, 50},
}

// Parse parses a colour string.
func Parse(s string) (Color, error) {
	in := strings.ToLower(strings.TrimSpace(s))
	switch {
	case in == "none" || in == "transparent":
		return Color{Model: None}, nil
	case strings.HasPrefix(in, "#"):
		return parseHex(in[1:], s)
	case strings.HasPrefix(in, "rgb(") && strings.HasSuffix(in, ")"):
		v, err := parseList(in[4:len(in)-1], 3, 255)
		if err != nil {
			return Color{}, fmt.Errorf("invalid color %q: %v", s, err)
		}
		return Color{Model: RGB, R: v[0], G: v[1], B: v[2]}, nil
	case strings.HasPrefix(in, "cmyk(") && strings.HasSuffix(in, ")"):
		v, err := parseList(in[5:len(in)-1], 4, 100)
		if err != nil {
			return Color{}, fmt.Errorf("invalid color %q: %v", s, err)
		}
		return Color{Model: CMYK, C: v[0], M: v[1], Y: v[2], K: v[3]}, nil
	}
	if rgb, ok := named[in]; ok {
		return Color{Model: RGB, R: rgb[0], G: rgb[1], B: rgb[2]}, nil
	}
	return Color{}, fmt.Errorf("invalid color %q (use #RRGGBB, rgb(r,g,b), cmyk(c,m,y,k), a CSS colour name or none)", s)
}

func parseHex(h, orig string) (Color, error) {
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return Color{}, fmt.Errorf("invalid color %q: expected #RRGGBB", orig)
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("invalid color %q: expected #RRGGBB", orig)
	}
	return Color{Model: RGB, R: int(n >> 16 & 0xff), G: int(n >> 8 & 0xff), B: int(n & 0xff)}, nil
}

func parseList(s string, n, max int) ([]int, error) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, fmt.Errorf("expected %d numbers", n)
	}
	out := make([]int, n)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p), "%")))
		if err != nil || v < 0 || v > max {
			return nil, fmt.Errorf("value %q must be 0–%d", strings.TrimSpace(p), max)
		}
		out[i] = v
	}
	return out, nil
}

// Hex returns "#RRGGBB" for RGB colours, "cmyk(...)" for CMYK and "none".
func (c Color) String() string {
	switch c.Model {
	case RGB:
		return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
	case CMYK:
		return fmt.Sprintf("cmyk(%d,%d,%d,%d)", c.C, c.M, c.Y, c.K)
	}
	return "none"
}

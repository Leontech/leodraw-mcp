// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package colors

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]string{
		"#ff0000":           "#FF0000",
		"#0F0":              "#00FF00",
		" Red ":             "#FF0000",
		"rgb(1, 2, 3)":      "#010203",
		"cmyk(0,100,100,0)": "cmyk(0,100,100,0)",
		"none":              "none",
	}
	for in, want := range ok {
		c, err := Parse(in)
		if err != nil || c.String() != want {
			t.Errorf("Parse(%q) = %v, %v; want %s", in, c, err, want)
		}
	}
	for _, bad := range []string{"", "#12", "rgb(1,2)", "rgb(300,0,0)", "cmyk(0,0,0,101)", "blurple"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

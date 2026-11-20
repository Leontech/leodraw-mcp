// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Schema is a JSON Schema fragment.
type Schema = map[string]any

// Object builds an object schema. props may be nil for tools without arguments.
func Object(required []string, props map[string]Schema) Schema {
	if props == nil {
		props = map[string]Schema{}
	}
	s := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// Number is a number property.
func Number(desc string) Schema { return Schema{"type": "number", "description": desc} }

// Integer is an integer property.
func Integer(desc string) Schema { return Schema{"type": "integer", "description": desc} }

// String is a string property.
func String(desc string) Schema { return Schema{"type": "string", "description": desc} }

// Boolean is a boolean property.
func Boolean(desc string) Schema { return Schema{"type": "boolean", "description": desc} }

// Enum is a string property restricted to values.
func Enum(desc string, values ...string) Schema {
	return Schema{"type": "string", "description": desc, "enum": values}
}

// Array is an array property.
func Array(desc string, items Schema) Schema {
	return Schema{"type": "array", "description": desc, "items": items}
}

// Default sets the default value of a property schema and returns it.
func Default(s Schema, v any) Schema { s["default"] = v; return s }

// Range sets minimum and maximum of a numeric property schema and returns it.
func Range(s Schema, min, max float64) Schema {
	s["minimum"] = min
	s["maximum"] = max
	return s
}

// Bind decodes tool arguments into v and rejects unknown fields, so that a
// misspelled parameter is reported to the model instead of silently ignored.
func Bind(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

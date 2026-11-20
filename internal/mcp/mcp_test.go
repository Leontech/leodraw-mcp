// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func newTestServer() *Server {
	s := NewServer(Info{Name: "test", Version: "1", Instructions: "hi"}, nil)
	type echoArgs struct {
		Text string `json:"text"`
	}
	s.AddTool(&Tool{
		Name:        "echo",
		Description: "echo",
		InputSchema: Object([]string{"text"}, map[string]Schema{"text": String("text")}),
		Handler: func(ctx context.Context, raw json.RawMessage) (*Result, error) {
			var a echoArgs
			if err := Bind(raw, &a); err != nil {
				return nil, err
			}
			return TextResult(a.Text), nil
		},
	})
	s.AddTool(&Tool{
		Name: "fail",
		Handler: func(ctx context.Context, raw json.RawMessage) (*Result, error) {
			return nil, errors.New("boom")
		},
	})
	s.AddTool(&Tool{
		Name: "panic",
		Handler: func(ctx context.Context, raw json.RawMessage) (*Result, error) {
			panic("oops")
		},
	})
	return s
}

func run(t *testing.T, input string) map[string]map[string]any {
	t.Helper()
	s := newTestServer()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), strings.NewReader(input), pw); pw.Close() }()
	out := map[string]map[string]any{}
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad json %q: %v", sc.Text(), err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("missing jsonrpc: %v", m)
		}
		id, _ := json.Marshal(m["id"])
		out[string(id)] = m
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return out
}

func TestProtocol(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"echo","arguments":{"text":"ahoj"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"txt":"x"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fail"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"panic","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":9,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":10,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`not json`,
	}, "\n") + "\n"
	out := run(t, in)

	init := out["1"]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" || init["instructions"] != "hi" {
		t.Errorf("initialize: %v", init)
	}
	if v := out["10"]["result"].(map[string]any)["protocolVersion"]; v != LatestProtocolVersion {
		t.Errorf("fallback version: %v", v)
	}
	tools := out["2"]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Errorf("tools/list: %v", tools)
	}
	if fail := tools[1].(map[string]any)["inputSchema"].(map[string]any); fail["type"] != "object" {
		t.Errorf("default schema: %v", fail)
	}
	text := func(id string) (string, bool) {
		r := out[id]["result"].(map[string]any)
		c := r["content"].([]any)[0].(map[string]any)
		isErr, _ := r["isError"].(bool)
		return c["text"].(string), isErr
	}
	if s, e := text(`"a"`); s != "ahoj" || e {
		t.Errorf("echo: %q %v", s, e)
	}
	if s, e := text("4"); !e || !strings.Contains(s, "txt") {
		t.Errorf("unknown field: %q %v", s, e)
	}
	if s, e := text("5"); !e || s != "boom" {
		t.Errorf("fail: %q %v", s, e)
	}
	if s, e := text("6"); !e || !strings.Contains(s, "oops") {
		t.Errorf("panic: %q %v", s, e)
	}
	if out["7"]["error"] == nil || out["8"]["error"] == nil {
		t.Errorf("errors expected: %v %v", out["7"], out["8"])
	}
	if out["9"]["result"] == nil {
		t.Errorf("ping: %v", out["9"])
	}
	if out["null"]["error"] == nil {
		t.Errorf("parse error expected")
	}
}

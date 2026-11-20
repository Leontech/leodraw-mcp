// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcp is a minimal, dependency-free Model Context Protocol server.
//
// It speaks JSON-RPC 2.0 over newline-delimited stdio and implements the
// subset of MCP needed by a tools-only server: initialize, ping, tools/list
// and tools/call. Notifications from the client are accepted and ignored.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"sync"
)

// LatestProtocolVersion is offered when the client asks for a version we do not know.
const LatestProtocolVersion = "2025-11-25"

var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Annotations are behavioural hints for clients (MCP tool annotations).
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// Handler runs a tool. args is the raw JSON object sent by the client.
// A returned error is reported to the model as a tool error (isError=true).
type Handler func(ctx context.Context, args json.RawMessage) (*Result, error)

// Tool is one MCP tool.
type Tool struct {
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description"`
	InputSchema Schema       `json:"inputSchema"`
	Annotations *Annotations `json:"annotations,omitempty"`
	Handler     Handler      `json:"-"`
	// Toolset is the group the tool belongs to (used for --toolsets filtering).
	Toolset string `json:"-"`
}

// Content is one item of a tool result.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Result is the result of tools/call.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// TextResult returns a plain text result.
func TextResult(text string) *Result {
	return &Result{Content: []Content{{Type: "text", Text: text}}}
}

// JSONResult returns an optional one-line summary followed by v as JSON.
func JSONResult(summary string, v any) *Result {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return ErrorResult("internal error: cannot encode result: " + err.Error())
	}
	text := string(b)
	if summary != "" {
		text = summary + "\n" + text
	}
	return TextResult(text)
}

// ImageResult returns an image followed by an optional caption.
func ImageResult(data []byte, mimeType, caption string) *Result {
	r := &Result{Content: []Content{{
		Type:     "image",
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}}}
	if caption != "" {
		r.Content = append(r.Content, Content{Type: "text", Text: caption})
	}
	return r
}

// ErrorResult returns a tool error the model can read and react to.
func ErrorResult(msg string) *Result {
	return &Result{Content: []Content{{Type: "text", Text: msg}}, IsError: true}
}

// Info describes the server.
type Info struct {
	Name         string
	Title        string
	Version      string
	WebsiteURL   string
	Instructions string
	// ToolsListChanged announces that the tool list can change at run time
	// (the server sends notifications/tools/list_changed).
	ToolsListChanged bool
}

// Server is an MCP server.
type Server struct {
	info  Info
	tools []*Tool
	index map[string]*Tool
	log   *log.Logger

	wmu sync.Mutex
	out io.Writer
	wg  sync.WaitGroup
}

// NewServer creates a server. logger may be nil.
func NewServer(info Info, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Server{info: info, index: map[string]*Tool{}, log: logger}
}

// AddTool registers a tool. It panics on a duplicate name (programming error).
func (s *Server) AddTool(t *Tool) {
	if _, dup := s.index[t.Name]; dup {
		panic("mcp: duplicate tool " + t.Name)
	}
	if t.InputSchema == nil {
		t.InputSchema = Object(nil, nil)
	}
	s.tools = append(s.tools, t)
	s.index[t.Name] = t
}

// Tool returns the registered tool with the given name, or nil.
func (s *Server) Tool(name string) *Tool { return s.index[name] }

// Tools returns the registered tools in registration order.
func (s *Server) Tools() []*Tool { return s.tools }

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from r and writes responses to w until r reaches EOF
// or ctx is cancelled. Tool calls run concurrently; responses are written
// whole, one per line.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.out = w
	br := bufio.NewReaderSize(r, 1<<20)
	type lineOrErr struct {
		line []byte
		err  error
	}
	lines := make(chan lineOrErr)
	go func() {
		for {
			line, err := br.ReadBytes('\n')
			lines <- lineOrErr{line, err}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return ctx.Err()
		case l := <-lines:
			if t := bytes.TrimSpace(l.line); len(t) > 0 {
				s.dispatch(ctx, t)
			}
			if l.err != nil {
				s.wg.Wait()
				if l.err == io.EOF {
					return nil
				}
				return l.err
			}
		}
	}
}

func (s *Server) dispatch(ctx context.Context, msg []byte) {
	if msg[0] == '[' { // JSON-RPC batch (older protocol versions)
		var batch []json.RawMessage
		if err := json.Unmarshal(msg, &batch); err != nil {
			s.writeError(json.RawMessage("null"), codeParseError, "parse error: "+err.Error())
			return
		}
		for _, m := range batch {
			s.handle(ctx, m)
		}
		return
	}
	s.handle(ctx, msg)
}

func (s *Server) handle(ctx context.Context, raw json.RawMessage) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeError(json.RawMessage("null"), codeParseError, "parse error: "+err.Error())
		return
	}
	if req.Method == "" {
		return // a response to a server request; we never send any
	}
	notification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		if !notification {
			s.reply(req.ID, s.initialize(req.Params))
		}
	case "ping":
		if !notification {
			s.reply(req.ID, map[string]any{})
		}
	case "tools/list":
		if !notification {
			tools := s.tools
			if tools == nil {
				tools = []*Tool{}
			}
			s.reply(req.ID, map[string]any{"tools": tools})
		}
	case "tools/call":
		if notification {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.callTool(ctx, req)
		}()
	default:
		if !notification {
			s.writeError(req.ID, codeMethodNotFound, "method not found: "+req.Method)
		}
	}
}

func (s *Server) initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(params, &p)
	version := LatestProtocolVersion
	for _, v := range supportedVersions {
		if v == p.ProtocolVersion {
			version = v
			break
		}
	}
	s.log.Printf("initialize: client=%s %s protocol=%s -> %s",
		p.ClientInfo.Name, p.ClientInfo.Version, p.ProtocolVersion, version)
	res := map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": s.info.ToolsListChanged},
		},
		"serverInfo": map[string]any{
			"name":    s.info.Name,
			"title":   s.info.Title,
			"version": s.info.Version,
		},
	}
	if s.info.WebsiteURL != "" {
		res["serverInfo"].(map[string]any)["websiteUrl"] = s.info.WebsiteURL
	}
	if s.info.Instructions != "" {
		res["instructions"] = s.info.Instructions
	}
	return res
}

func (s *Server) callTool(ctx context.Context, req request) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(req.ID, codeInvalidParams, "invalid params: "+err.Error())
		return
	}
	t, ok := s.index[p.Name]
	if !ok {
		s.writeError(req.ID, codeInvalidParams, "unknown tool: "+p.Name)
		return
	}
	if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
		p.Arguments = json.RawMessage("{}")
	}
	s.log.Printf("call %s %s", p.Name, truncate(string(p.Arguments), 500))
	res := s.runHandler(ctx, t, p.Arguments)
	if res.IsError && len(res.Content) > 0 {
		s.log.Printf("call %s failed: %s", p.Name, truncate(res.Content[0].Text, 500))
	}
	s.reply(req.ID, res)
}

func (s *Server) runHandler(ctx context.Context, t *Tool, args json.RawMessage) (res *Result) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("panic in tool %s: %v\n%s", t.Name, r, debug.Stack())
			res = ErrorResult(fmt.Sprintf("internal error in %s: %v", t.Name, r))
		}
	}()
	res, err := t.Handler(ctx, args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if res == nil {
		return TextResult("OK")
	}
	return res
}

func (s *Server) reply(id json.RawMessage, result any) {
	s.write(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, code int, msg string) {
	s.write(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.log.Printf("cannot encode response: %v", err)
		b, _ = json.Marshal(response{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: codeInternalError, Message: "cannot encode response"}})
	}
	b = append(b, '\n')
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.out.Write(b); err != nil {
		s.log.Printf("write failed: %v", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FirstText returns the first text content of the result ("" if none).
func (r *Result) FirstText() string {
	for _, c := range r.Content {
		if c.Type == "text" {
			return c.Text
		}
	}
	return ""
}

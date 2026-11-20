//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

// The window shown when leodraw-mcp.exe is double-clicked: registration in
// Claude Desktop, status and links. It is an HTML page served on 127.0.0.1
// (random port + token) and shown in Microsoft Edge's app mode (a plain window
// without address bar). No dependencies besides the Go standard library.

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/tools"
)

//go:embed gui.html
var guiHTML string

//go:embed leotech.svg
var leotechSVG string

var (
	modkernel32                  = syscall.NewLazyDLL("kernel32.dll")
	modshell32                   = syscall.NewLazyDLL("shell32.dll")
	procGetConsoleProcessList    = modkernel32.NewProc("GetConsoleProcessList")
	procFreeConsole              = modkernel32.NewProc("FreeConsole")
	procGetUserDefaultUILanguage = modkernel32.NewProc("GetUserDefaultUILanguage")
	procShellExecuteW            = modshell32.NewProc("ShellExecuteW")
)

// startedByClient reports whether stdin is a pipe, i.e. an MCP client
// (Claude Desktop, Claude Code …) started us. A double-click gives a console.
func startedByClient() bool {
	t, err := syscall.GetFileType(syscall.Stdin)
	return err == nil && t == syscall.FILE_TYPE_PIPE
}

// hideOwnConsole closes the console window Windows opened for a double-click
// (only when no other process, e.g. a terminal, shares it).
func hideOwnConsole() {
	var ids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&ids[0])), 4)
	if n == 1 {
		procFreeConsole.Call()
	}
}

func uiLanguage() string {
	id, _, _ := procGetUserDefaultUILanguage.Call()
	switch id & 0x3ff {
	case 0x05, 0x1b: // Czech, Slovak
		return "cs"
	}
	return "en"
}

func openURL(url string) {
	verb, _ := syscall.UTF16PtrFromString("open")
	u, _ := syscall.UTF16PtrFromString(url)
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(u)), 0, 0, 1)
}

func findEdge() string {
	var cands []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if d := os.Getenv(env); d != "" {
			cands = append(cands, filepath.Join(d, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if d := os.Getenv(env); d != "" {
			cands = append(cands, filepath.Join(d, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// openWindow shows url as an app window (Edge/Chrome --app), else in the default browser.
func openWindow(url string) {
	if browser := findEdge(); browser != "" {
		profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "LeoDraw", "window")
		cmd := exec.Command(browser,
			"--app="+url,
			"--window-size=1040,720",
			"--user-data-dir="+profile,
			"--no-first-run", "--no-default-browser-check",
			"--disable-features=Translate,msEdgeSidebarV2,msHubApps",
		)
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return
		}
	}
	openURL(url)
}

type gui struct {
	lang string
	sess *corel.Session

	mu       sync.Mutex
	lastPing time.Time
	byeAt    time.Time
}

type corelState struct {
	Running bool   `json:"running"`
	Version string `json:"version,omitempty"`
	Process bool   `json:"process"`         // a CorelDRW.exe process exists
	Error   string `json:"error,omitempty"` // why connecting failed
}

type guiState struct {
	Version   string            `json:"version"`
	Lang      string            `json:"lang"`
	Reg       regState          `json:"reg"`
	Corel     corelState        `json:"corel"`
	ClaudeCmd string            `json:"claude_code_cmd"`
	Manual    string            `json:"manual_json"`  // entry for "mcpServers" when editing the config by hand
	Tested    string            `json:"tested_corel"` // CorelDRAW version LeoDraw was tested with
	Links     map[string]string `json:"links"`
}

// testedCorel is the CorelDRAW build this LeoDraw version was tested with
// (major version 27 = CorelDRAW 2026).
const testedCorel = "27.2.0.135"

func (g *gui) state(ctx context.Context) guiState {
	st := guiState{Version: version, Lang: g.lang, Reg: registrationState(), Links: tools.About}
	st.ClaudeCmd = claudeCodeCommand(st.Reg.Exe)
	st.Manual = manualConfigEntry(st.Reg.Exe)
	st.Tested = testedCorel
	if g.sess != nil {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		err := g.sess.Run(ctx, func(c *corel.Ctx) error {
			st.Corel = corelState{Running: true, Version: c.Info().Version}
			return nil
		})
		if err != nil {
			st.Corel.Error = g.sess.LastError()
			if st.Corel.Error == "" {
				st.Corel.Error = err.Error()
			}
		}
	}
	if !st.Corel.Running {
		st.Corel.Process = corel.ProcessRunning()
	}
	return st
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// runGUI shows the window and returns when it is closed.
func runGUI() int {
	hideOwnConsole()
	g := &gui{lang: uiLanguage()}
	logger := log.New(io.Discard, "", 0)
	if s, err := corel.NewSession(corel.Options{Logger: logger}); err == nil {
		g.sess = s
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 1
	}
	port := ln.Addr().(*net.TCPAddr).Port
	host := fmt.Sprintf("127.0.0.1:%d", port)
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	base := "/" + hex.EncodeToString(tok)

	mux := http.NewServeMux()
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != base+"/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, guiHTML)
	})
	mux.HandleFunc(base+"/leotech.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, leotechSVG)
	})
	mux.HandleFunc(base+"/api/state", func(w http.ResponseWriter, r *http.Request) {
		g.touch()
		writeJSON(w, g.state(r.Context()))
	})
	register := func(install bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			_, res, err := applyRegistration(install)
			out := map[string]any{"ok": err == nil, "results": res}
			if err != nil {
				out["error"] = err.Error()
			}
			writeJSON(w, out)
		}
	}
	mux.HandleFunc(base+"/api/register", register(true))
	mux.HandleFunc(base+"/api/unregister", register(false))
	mux.HandleFunc(base+"/api/open", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil ||
			!(strings.HasPrefix(req.URL, "https://") || strings.HasPrefix(req.URL, "http://")) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		openURL(req.URL)
		writeJSON(w, map[string]bool{"ok": true})
	})
	// reveal shows a Claude Desktop configuration file in Explorer (only the known config files).
	mux.HandleFunc(base+"/api/reveal", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		known := false
		for _, c := range registrationState().Configs {
			if strings.EqualFold(c.Path, req.Path) {
				known = true
			}
		}
		if !known {
			http.Error(w, "unknown file", http.StatusBadRequest)
			return
		}
		cmd := exec.Command("explorer.exe", "/select,"+req.Path)
		if err := cmd.Start(); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		go cmd.Wait()
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc(base+"/api/ping", func(w http.ResponseWriter, r *http.Request) {
		g.touch()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(base+"/api/bye", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.byeAt = time.Now()
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != host { // DNS rebinding protection
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			mux.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(ln)
	openWindow("http://" + host + base + "/")

	g.waitClosed()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	return 0
}

func (g *gui) touch() {
	g.mu.Lock()
	g.lastPing = time.Now()
	g.byeAt = time.Time{}
	g.mu.Unlock()
}

// waitClosed returns when the page stopped pinging (window closed): 3 s after
// an unload beacon, 12 s without pings, or 90 s if the page never loaded.
func (g *gui) waitClosed() {
	start := time.Now()
	for range time.Tick(500 * time.Millisecond) {
		g.mu.Lock()
		last, bye := g.lastPing, g.byeAt
		g.mu.Unlock()
		switch {
		case !bye.IsZero() && time.Since(bye) > 3*time.Second:
			return
		case last.IsZero() && time.Since(start) > 90*time.Second:
			return
		case !last.IsZero() && time.Since(last) > 12*time.Second:
			return
		}
	}
}

//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const serverKey = "leodraw"

// claudeConfigPaths returns candidate Claude Desktop config files:
//   - %APPDATA%\Claude                                    (classic installer)
//   - %LOCALAPPDATA%\Claude-Data\<package id>             (current Microsoft Store / MSIX builds)
//   - %LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude (older MSIX builds, virtualised AppData)
func claudeConfigPaths() []string {
	const name = "claude_desktop_config.json"
	var out []string
	if a := os.Getenv("APPDATA"); a != "" {
		out = append(out, filepath.Join(a, "Claude", name))
	}
	if l := os.Getenv("LOCALAPPDATA"); l != "" {
		if fi, err := os.Stat(filepath.Join(l, "Claude-Data", name)); err == nil && !fi.IsDir() {
			out = append(out, filepath.Join(l, "Claude-Data", name))
		}
		dirs, _ := filepath.Glob(filepath.Join(l, "Claude-Data", "*"))
		for _, d := range dirs {
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				out = append(out, filepath.Join(d, name))
			}
		}
		dirs, _ = filepath.Glob(filepath.Join(l, "Packages", "Claude_*", "LocalCache", "Roaming", "Claude"))
		for _, d := range dirs {
			out = append(out, filepath.Join(d, name))
		}
	}
	return out
}

// existingConfigs returns config paths whose folder exists (Claude Desktop installed).
func existingConfigs() []string {
	var out []string
	for _, p := range claudeConfigPaths() {
		if fi, err := os.Stat(filepath.Dir(p)); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// orderedObject is a JSON object that keeps its key order (so the user's
// config is not reshuffled).
type orderedObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(b []byte) (*orderedObject, error) {
	o := &orderedObject{vals: map[string]json.RawMessage{}}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errors.New("expected a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	return o, nil
}

func (o *orderedObject) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *orderedObject) del(k string) bool {
	if _, ok := o.vals[k]; !ok {
		return false
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

func (o *orderedObject) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// registeredCommand returns the command registered for LeoDraw in a config ("" if none).
func registeredCommand(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	root, err := parseObject(b)
	if err != nil {
		return "", fmt.Errorf("%s is not valid JSON: %v", path, err)
	}
	servers, err := parseObject(root.vals["mcpServers"])
	if err != nil {
		return "", err
	}
	entry, ok := servers.vals[serverKey]
	if !ok {
		return "", nil
	}
	var e struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(entry, &e)
	if e.Command == "" {
		e.Command = "?"
	}
	return e.Command, nil
}

// updateConfig adds/updates (install=true) or removes the LeoDraw entry,
// keeping everything else and writing a backup first.
func updateConfig(path, exe string, install bool) (changed bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	root, err := parseObject(b)
	if err != nil {
		return false, fmt.Errorf("%s is not valid JSON (%v) – fix or delete it first", path, err)
	}
	servers, err := parseObject(root.vals["mcpServers"])
	if err != nil {
		return false, fmt.Errorf("mcpServers in %s is not an object: %v", path, err)
	}
	if install {
		entry, err := parseObject(servers.vals[serverKey]) // keeps user's args/env
		if err != nil {
			entry = &orderedObject{vals: map[string]json.RawMessage{}}
		}
		cmd, _ := json.Marshal(exe)
		if bytes.Equal(entry.vals["command"], cmd) {
			return false, nil
		}
		entry.set("command", cmd)
		eb, err := entry.marshal()
		if err != nil {
			return false, err
		}
		servers.set(serverKey, eb)
	} else if !servers.del(serverKey) {
		return false, nil
	}
	sb, err := servers.marshal()
	if err != nil {
		return false, err
	}
	root.set("mcpServers", sb)
	out, err := root.marshal()
	if err != nil {
		return false, err
	}
	if len(b) > 0 {
		backup := path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, b, 0o644); err != nil {
			return false, fmt.Errorf("cannot write backup %s: %v", backup, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// regResult is the outcome of (un)registering in one config file.
type regResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Error   string `json:"error,omitempty"`
}

// applyRegistration registers (install=true) or unregisters LeoDraw in every
// Claude Desktop config found on this PC.
func applyRegistration(install bool) (string, []regResult, error) {
	exe, err := exePath()
	if err != nil {
		return "", nil, fmt.Errorf("cannot find own path: %v", err)
	}
	paths := existingConfigs()
	if len(paths) == 0 {
		if !install {
			return exe, nil, nil
		}
		paths = claudeConfigPaths()[:1]
	}
	var res []regResult
	var firstErr error
	for _, p := range paths {
		changed, err := updateConfig(p, exe, install)
		r := regResult{Path: p, Changed: changed}
		if err != nil {
			r.Error = err.Error()
			if firstErr == nil {
				firstErr = err
			}
		}
		res = append(res, r)
	}
	return exe, res, firstErr
}

// cfgState describes one Claude Desktop config file.
type cfgState struct {
	Path     string `json:"path"`
	Command  string `json:"command,omitempty"` // registered LeoDraw command, "" = not registered
	Error    string `json:"error,omitempty"`
	Active   bool   `json:"active"`              // the copy Claude Desktop is using (newest activity)
	Stale    bool   `json:"stale"`               // another copy is the active one – Claude does not read this one
	LastUsed string `json:"last_used,omitempty"` // RFC 3339 time of Claude's last activity in that folder
}

// configActivity returns when Claude Desktop last worked in the folder of a
// config file: the newest of its log files and Electron state files. The
// config file itself is left out, LeoDraw writes it.
func configActivity(cfgPath string) time.Time {
	dir := filepath.Dir(cfgPath)
	var t time.Time
	check := func(p string) {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "logs", "*.log"))
	for _, l := range logs {
		check(l)
	}
	for _, n := range []string{"Local State", "Preferences", "config.json", filepath.Join("Network", "Cookies"), "Cookies"} {
		check(filepath.Join(dir, n))
	}
	return t
}

// markActive marks the config Claude Desktop uses: with several copies (e.g.
// after an update from an older Microsoft Store build) only the folder with
// the newest activity is the live one.
func markActive(cs []cfgState) {
	if len(cs) == 0 {
		return
	}
	best, bestT := -1, time.Time{}
	for i := range cs {
		t := configActivity(cs[i].Path)
		if !t.IsZero() {
			cs[i].LastUsed = t.Format(time.RFC3339)
		}
		if t.After(bestT) {
			best, bestT = i, t
		}
	}
	if best < 0 {
		return // no activity anywhere – cannot tell
	}
	for i := range cs {
		cs[i].Active = i == best
		cs[i].Stale = len(cs) > 1 && i != best
	}
	// the live config first
	if best > 0 {
		a := cs[best]
		copy(cs[1:best+1], cs[:best])
		cs[0] = a
	}
}

// regState is the registration overview shown in the window.
type regState struct {
	Exe             string     `json:"exe"`
	ClaudeFound     bool       `json:"claude_found"`
	Configs         []cfgState `json:"configs"`
	Registered      bool       `json:"registered"`       // registered with this exe everywhere
	RegisteredOther string     `json:"registered_other"` // registered, but another exe path
}

func registrationState() regState {
	st := regState{Configs: []cfgState{}}
	st.Exe, _ = exePath()
	paths := existingConfigs()
	st.ClaudeFound = len(paths) > 0
	st.Registered = st.ClaudeFound
	for _, p := range paths {
		cmd, err := registeredCommand(p)
		c := cfgState{Path: p, Command: cmd}
		if err != nil {
			c.Error = err.Error()
		}
		st.Configs = append(st.Configs, c)
		if !strings.EqualFold(cmd, st.Exe) {
			st.Registered = false
			if cmd != "" && st.RegisteredOther == "" {
				st.RegisteredOther = cmd
			}
		}
	}
	markActive(st.Configs)
	return st
}

// runInstall implements --install / --uninstall. Returns the process exit code.
func runInstall(install bool) int {
	if install && len(existingConfigs()) == 0 {
		fmt.Println("Claude Desktop does not seem to be installed; writing the configuration anyway.")
	}
	exe, res, err := applyRegistration(install)
	if exe == "" {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(res) == 0 {
		fmt.Println("Claude Desktop configuration not found – nothing to remove.")
		return 0
	}
	for _, r := range res {
		switch {
		case r.Error != "":
			fmt.Fprintln(os.Stderr, "ERROR:", r.Error)
		case !r.Changed && install:
			fmt.Printf("Already registered in %s\n", r.Path)
		case !r.Changed:
			fmt.Printf("Not registered in %s\n", r.Path)
		case install:
			fmt.Printf("Registered in %s\n  command: %s\n", r.Path, exe)
		default:
			fmt.Printf("Removed from %s\n", r.Path)
		}
	}
	if err != nil {
		return 1
	}
	fmt.Println("\nQuit Claude Desktop completely (also from the system tray) and start it again.")
	fmt.Println("Claude Code users: " + claudeCodeCommand(exe))
	return 0
}

func claudeCodeCommand(exe string) string {
	return `claude mcp add leodraw -- "` + exe + `"`
}

// manualConfigEntry is the entry for a hand-edited claude_desktop_config.json
// (goes inside "mcpServers").
func manualConfigEntry(exe string) string {
	b, _ := json.Marshal(exe)
	return `"` + serverKey + `": {` + "\n" + `  "command": ` + string(b) + "\n" + `}`
}

//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command leodraw-mcp is an MCP server that lets AI assistants such as
// Claude control CorelDRAW on Windows.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Leontech/leodraw-mcp/internal/corel"
	"github.com/Leontech/leodraw-mcp/internal/mcp"
	"github.com/Leontech/leodraw-mcp/internal/tools"
)

// appVersion is the released version. Change it here for a new release:
// GitHub Actions reads it and publishes release vX.Y.Z.
const appVersion = "0.2.0"

// version can still be overridden at build time with -ldflags "-X main.version=…".
var version = appVersion

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		listTools   = flag.Bool("list-tools", false, "print the available tools and exit")
		launch      = flag.Bool("launch", false, "start CorelDRAW automatically when it is not running")
		progIDs     = flag.String("progid", "", "comma-separated COM ProgIDs to try (default: CorelDRAW.Application.27,CorelDRAW.Application)")
		toolsets    = flag.String("toolsets", "", "comma-separated toolsets to enable (default: all except advanced)")
		allowRaw    = flag.Bool("allow-raw-com", false, "enable the advanced raw COM tools (can do anything CorelDRAW can, including running macros)")
		allowMacros = flag.Bool("allow-macros", false, "enable running GMS/VBA macros (macros can run any program on this PC)")
		install     = flag.Bool("install", false, "register LeoDraw in Claude Desktop (claude_desktop_config.json, a backup is made) and exit")
		uninstall   = flag.Bool("uninstall", false, "remove LeoDraw from Claude Desktop and exit")
		child       = flag.Bool("child", false, "internal: run as the server process started by the supervisor")
		noSuper     = flag.Bool("no-supervisor", false, "serve MCP directly (no supervisor process, no server_reload)")
		showGUI     = flag.Bool("gui", false, "show the registration window (default when the exe is double-clicked)")
		logFile     = flag.String("log-file", "", "log file (default: %LOCALAPPDATA%\\LeoDraw\\logs\\leodraw-mcp.log; \"-\" = stderr)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "LeoDraw MCP %s – MCP server for CorelDRAW (stdio)\n\nUsage: leodraw-mcp [options]\n\n", version)
		flag.PrintDefaults()
		fmt.Fprint(os.Stderr, aboutText())
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("leodraw-mcp", version)
		fmt.Print(aboutText())
		return
	}

	switch {
	case *install || *uninstall:
		os.Exit(runInstall(*install))
	case *showGUI || (!*child && flag.NFlag() == 0 && !startedByClient()):
		os.Exit(runGUI()) // double-clicked
	}

	logger := openLog(*logFile)

	if !*child && !*noSuper && startedByClient() && !*listTools {
		exe, err := exePath()
		if err == nil {
			logger.Printf("LeoDraw MCP %s supervisor starting (pid %d)", version, os.Getpid())
			sup := &supervisor{exe: exe, args: os.Args[1:], log: logger, out: os.Stdout}
			os.Exit(sup.run())
		}
		logger.Printf("supervisor unavailable (%v) – serving directly", err)
	}
	logger.Printf("LeoDraw MCP %s starting (pid %d)", version, os.Getpid())

	deps := &tools.Deps{Log: logger, AllowRawCOM: *allowRaw, AllowMacros: *allowMacros, Supervised: *child}
	if *toolsets != "" {
		deps.Toolsets = map[string]bool{}
		for _, t := range strings.Split(*toolsets, ",") {
			t = strings.TrimSpace(t)
			if _, ok := tools.Toolsets[t]; !ok {
				fmt.Fprintf(os.Stderr, "unknown toolset %q (known: %s)\n", t, strings.Join(toolsetNames(), ", "))
				os.Exit(2)
			}
			deps.Toolsets[t] = true
		}
	}

	srv := mcp.NewServer(mcp.Info{
		Name:             "leodraw-mcp",
		Title:            "LeoDraw MCP for CorelDRAW",
		Version:          version,
		WebsiteURL:       tools.About["web"],
		Instructions:     tools.Instructions,
		ToolsListChanged: *child,
	}, logger)

	if *listTools {
		tools.Register(srv, deps)
		for _, t := range srv.Tools() {
			fmt.Printf("%-20s [%s] %s\n", t.Name, t.Toolset, firstSentence(t.Description))
		}
		return
	}

	opt := corel.Options{Launch: *launch, Logger: logger}
	if *progIDs != "" {
		opt.ProgIDs = strings.Split(*progIDs, ",")
	}
	sess, err := corel.NewSession(opt)
	if err != nil {
		logger.Printf("cannot start COM: %v", err)
		fmt.Fprintln(os.Stderr, "cannot start COM:", err)
		os.Exit(1)
	}
	deps.S = sess
	tools.Register(srv, deps)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		logger.Printf("server stopped: %v", err)
		os.Exit(1)
	}
	logger.Printf("stdin closed, exiting")
}

func openLog(path string) *log.Logger {
	if path == "-" {
		return log.New(os.Stderr, "", log.LstdFlags)
	}
	if path == "" {
		dir := os.Getenv("LOCALAPPDATA")
		if dir == "" {
			return log.New(io.Discard, "", 0)
		}
		path = filepath.Join(dir, "LeoDraw", "logs", "leodraw-mcp.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return log.New(io.Discard, "", 0)
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return log.New(io.Discard, "", 0)
	}
	return log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func toolsetNames() []string {
	var n []string
	for k := range tools.Toolsets {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func aboutText() string {
	a := tools.About
	return fmt.Sprintf("\n%s © 2026 %s – %s\nLicense: GNU GPL v3 or later (free software, no warranty)\nSource:  %s\nSupport: %s ☕\n\nUnofficial project, not affiliated with Corel Corporation or Anthropic.\nCorelDRAW is a trademark of Corel Corporation; Claude is a trademark of Anthropic, PBC.\n", a["name"], a["author"], a["web"], a["source"], a["support"])
}

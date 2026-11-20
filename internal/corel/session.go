//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package corel wraps the CorelDRAW automation object model.
package corel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/Leontech/leodraw-mcp/internal/com"
)

// DefaultProgIDs are tried in order when connecting. 27 = CorelDRAW 2026.
var DefaultProgIDs = []string{"CorelDRAW.Application.27", "CorelDRAW.Application"}

var versionRx = regexp.MustCompile(`\d+(\.\d+)+`)

// ErrNotRunning means no CorelDRAW instance could be found.
var ErrNotRunning = errors.New("CorelDRAW is not running – start CorelDRAW and try again (or call corel_launch)")

// ErrNoDocument means CorelDRAW has no open document.
var ErrNoDocument = errors.New("no document is open in CorelDRAW – open one or call new_document")

// Options configure a Session.
type Options struct {
	ProgIDs []string // defaults to DefaultProgIDs
	Launch  bool     // start CorelDRAW automatically when it is not running
	Logger  *log.Logger
}

// Session is a (lazy) connection to CorelDRAW running on the COM worker thread.
type Session struct {
	opt      Options
	w        *com.Worker
	app      *com.Dispatch // only touched on the worker thread
	progID   string
	version  string
	launched bool // we started CorelDRAW: keep our reference so it does not quit
	lastErr  string
	batch    atomic.Bool // a batch holds one undo group: Write must not open its own
}

// SetBatch marks that a batch is running (its tools must not open undo groups).
func (s *Session) SetBatch(on bool) { s.batch.Store(on) }

// LastError describes why the last connection attempt failed ("" = connected).
// Call it after Run/Launch returned (it is written on the worker thread).
func (s *Session) LastError() string { return s.lastErr }

// NewSession starts the COM worker. It does not connect yet.
func NewSession(opt Options) (*Session, error) {
	if len(opt.ProgIDs) == 0 {
		opt.ProgIDs = DefaultProgIDs
	}
	if opt.Logger == nil {
		opt.Logger = log.Default()
	}
	w, err := com.NewWorker()
	if err != nil {
		return nil, err
	}
	return &Session{opt: opt, w: w}, nil
}

// Info about the connected application.
type Info struct {
	ProgID  string `json:"prog_id"`
	Version string `json:"version"`
}

// Run executes fn on the COM thread with a connected CorelDRAW.
// If CorelDRAW was restarted since the last call, it reconnects once.
//
// The reference to CorelDRAW is released after every call (unless we
// launched it), so that closing CorelDRAW is never blocked by LeoDraw
// holding it in memory.
func (s *Session) Run(ctx context.Context, fn func(c *Ctx) error) error {
	return s.w.Do(ctx, func() error {
		defer func() {
			// During a batch the connection stays open: CorelDRAW ends an open
			// command group (undo step) when its automation client disconnects.
			if !s.launched && !s.batch.Load() {
				s.drop()
			}
		}()
		if err := s.ensure(false); err != nil {
			return err
		}
		err := fn(&Ctx{s: s, App: s.app})
		if err != nil && com.IsDisconnected(err) {
			s.opt.Logger.Printf("CorelDRAW disconnected, reconnecting: %v", err)
			s.drop()
			if err := s.ensure(false); err != nil {
				return err
			}
			err = fn(&Ctx{s: s, App: s.app})
		}
		return err
	})
}

// Launch starts CorelDRAW if needed and makes it visible.
func (s *Session) Launch(ctx context.Context) (Info, error) {
	var info Info
	err := s.w.Do(ctx, func() error {
		if s.app != nil {
			if _, err := s.app.Get("Version"); err != nil && com.IsDisconnected(err) {
				s.drop()
			}
		}
		if err := s.ensure(true); err != nil {
			return err
		}
		info = Info{ProgID: s.progID, Version: s.version}
		if !s.launched {
			s.drop()
		}
		return nil
	})
	return info, err
}

func (s *Session) ensure(launch bool) error {
	if s.app != nil {
		return nil
	}
	var lastErr error
	var details []string
	for _, id := range s.opt.ProgIDs {
		d, err := com.GetActiveObject(id)
		if err == nil {
			s.app, s.progID = d.Keep(), id
			break
		}
		lastErr = err
		details = append(details, err.Error())
	}
	// Some CorelDRAW installations do not register in the Running Object
	// Table (GetActiveObject → MK_E_UNAVAILABLE). When CorelDRW.exe is running,
	// CoCreateInstance connects to that instance through its class factory.
	if before := ProcessCount(); s.app == nil && before > 0 {
		for _, id := range s.opt.ProgIDs {
			d, err := com.CreateObject(id)
			if err != nil {
				details = append(details, err.Error())
				lastErr = err
				continue
			}
			// Visible is false e.g. while the welcome screen is shown, so only a
			// new process means CoCreateInstance started a hidden instance.
			if vis, verr := d.GetBool("Visible"); verr == nil && !vis && ProcessCount() > before {
				// a new hidden instance was started instead – let it go
				d.Release()
				details = append(details, "CoCreateInstance "+id+" started a hidden instance instead of connecting to the running CorelDRAW")
				continue
			}
			s.app, s.progID = d.Keep(), id
			s.opt.Logger.Printf("connected via CoCreateInstance (CorelDRAW is not in the ROT)")
			break
		}
	}
	if s.app == nil && (launch || s.opt.Launch) {
		for _, id := range s.opt.ProgIDs {
			d, err := com.CreateObject(id)
			if err == nil {
				s.app, s.progID = d.Keep(), id
				s.launched = true
				_ = s.app.Put("Visible", true)
				break
			}
			lastErr = err
			details = append(details, err.Error())
		}
	}
	if s.app == nil {
		s.lastErr = strings.Join(details, "; ")
		s.opt.Logger.Printf("connect failed: %v", lastErr)
		if lastErr != nil && !com.IsNotRunning(lastErr) {
			return fmt.Errorf("%w (%v)", ErrNotRunning, lastErr)
		}
		return ErrNotRunning
	}
	s.lastErr = ""
	if s.version == "" {
		v, _ := s.app.GetString("Version")
		if m := versionRx.FindString(v); m != "" { // localized: "Verze 27.2.0.135"
			v = m
		}
		s.version = v
		s.opt.Logger.Printf("connected to %s, version %s", s.progID, s.version)
	}
	return nil
}

func (s *Session) drop() {
	if s.app != nil {
		s.app.Release()
		s.app = nil
		s.launched = false
	}
}

// Ctx gives tool code access to CorelDRAW inside Session.Run.
type Ctx struct {
	s   *Session
	App *com.Dispatch
}

// Logf writes to the server log.
func (c *Ctx) Logf(format string, args ...any) { c.s.opt.Logger.Printf(format, args...) }

// Info returns the connected application info.
func (c *Ctx) Info() Info { return Info{ProgID: c.s.progID, Version: c.s.version} }

// ActiveDocument returns the active document or ErrNoDocument.
func (c *Ctx) ActiveDocument() (*com.Dispatch, error) {
	d, err := c.App.GetObjOrNil("ActiveDocument")
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrNoDocument
	}
	return d, nil
}

// WithMM runs fn with the document's automation unit set to millimetres
// (Document.Unit affects automation only, not the rulers) and restores it.
func (c *Ctx) WithMM(doc *com.Dispatch, fn func() error) error {
	old, err := doc.Get("Unit")
	if err != nil {
		return err
	}
	if err := doc.Put("Unit", cdrMillimeter); err != nil {
		return err
	}
	defer func() { _ = doc.Put("Unit", old) }()
	return fn()
}

// Write wraps a modifying operation: one undo step named "LeoDraw: <name>",
// screen updates suspended while it runs.
func (c *Ctx) Write(doc *com.Dispatch, name string, fn func() error) error {
	if c.s.batch.Load() {
		// CorelDRAW does not nest command groups: inside a batch the batch's own
		// group collects everything into one undo step.
		return fn()
	}
	if _, err := doc.Call("BeginCommandGroup", "LeoDraw: "+name); err != nil {
		return err
	}
	_ = c.App.Put("Optimization", true)
	defer func() {
		_ = c.App.Put("Optimization", false)
		_, _ = doc.Call("EndCommandGroup")
		_, _ = c.App.Call("Refresh")
	}()
	return fn()
}

// Page returns page index (1-based) of doc, or the active page when index <= 0.
func (c *Ctx) Page(doc *com.Dispatch, index int) (*com.Dispatch, int, error) {
	if index <= 0 {
		p, err := doc.GetObj("ActivePage")
		if err != nil {
			return nil, 0, err
		}
		i, err := p.GetInt("Index")
		return p, int(i), err
	}
	pages, err := doc.GetObj("Pages")
	if err != nil {
		return nil, 0, err
	}
	n, err := pages.GetInt("Count")
	if err != nil {
		return nil, 0, err
	}
	if int64(index) > n {
		return nil, 0, fmt.Errorf("page %d does not exist (the document has %d pages)", index, n)
	}
	p, err := pages.GetObj("Item", index)
	return p, index, err
}

// Layer returns the named layer of page, or the document's active layer when name is empty.
func (c *Ctx) Layer(doc, page *com.Dispatch, name string) (*com.Dispatch, error) {
	if name == "" {
		return doc.GetObj("ActiveLayer")
	}
	layers, err := page.GetObj("Layers")
	if err != nil {
		return nil, err
	}
	n, err := layers.GetInt("Count")
	if err != nil {
		return nil, err
	}
	var names []string
	for i := int64(1); i <= n; i++ {
		l, err := layers.GetObj("Item", i)
		if err != nil {
			return nil, err
		}
		ln, _ := l.GetString("Name")
		if strings.EqualFold(ln, name) {
			return l, nil
		}
		names = append(names, ln)
	}
	return nil, fmt.Errorf("layer %q not found on this page (layers: %s)", name, strings.Join(names, ", "))
}

// Collect returns all items of a COM collection (Count / Item(i), 1-based).
func Collect(coll *com.Dispatch, limit int) ([]*com.Dispatch, int, error) {
	n, err := coll.GetInt("Count")
	if err != nil {
		return nil, 0, err
	}
	total := int(n)
	if limit <= 0 || limit > total {
		limit = total
	}
	out := make([]*com.Dispatch, 0, limit)
	for i := 1; i <= limit; i++ {
		it, err := coll.GetObj("Item", i)
		if err != nil {
			return nil, total, err
		}
		out = append(out, it)
	}
	return out, total, nil
}

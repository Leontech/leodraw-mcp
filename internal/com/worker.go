//go:build windows && (amd64 || arm64)

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package com

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"time"
)

type callInfo struct {
	name  string
	start time.Time
}

var current atomic.Pointer[callInfo]

func setCurrent(name string) {
	if name == "" {
		current.Store(nil)
		return
	}
	current.Store(&callInfo{name, time.Now()})
}

// CurrentCall describes the COM call in progress, e.g. "CreateDocument (65 s)", or "".
func CurrentCall() string {
	c := current.Load()
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%s (%.0f s)", c.name, time.Since(c.start).Seconds())
}

func stuckIn() string {
	if c := CurrentCall(); c != "" {
		return "; waiting for CorelDRAW in " + c
	}
	return ""
}

// Worker owns a single OS thread initialised as a COM single-threaded
// apartment. All COM work is submitted to it with Do.
type Worker struct {
	jobs chan *job
}

type job struct {
	fn   func() error
	done chan error
}

// NewWorker starts the COM thread.
func NewWorker() (*Worker, error) {
	w := &Worker{jobs: make(chan *job)}
	ready := make(chan error, 1)
	go w.loop(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Worker) loop(ready chan<- error) {
	runtime.LockOSThread() // never unlocked: the thread belongs to COM
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if failed(hr) && uint32(hr) != rpcEChangedMode {
		ready <- &Error{Op: "CoInitializeEx", HRESULT: uint32(hr)}
		return
	}
	ready <- nil
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case j, ok := <-w.jobs:
			if !ok {
				procCoUninitialize.Call()
				return
			}
			sc := &scope{}
			currentScope = sc
			err := runJob(j.fn)
			currentScope = nil
			sc.releaseAll()
			pumpMessages()
			j.done <- err
		case <-tick.C:
			pumpMessages()
		}
	}
}

func runJob(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v\n%s", r, debug.Stack())
		}
	}()
	return fn()
}

// Do runs fn on the COM thread and waits for it. If ctx ends first, Do
// returns an error; fn keeps running and later jobs wait for it.
func (w *Worker) Do(ctx context.Context, fn func() error) error {
	j := &job{fn: fn, done: make(chan error, 1)}
	select {
	case w.jobs <- j:
	case <-ctx.Done():
		return fmt.Errorf("CorelDRAW is still busy with a previous operation (is a dialog open in CorelDRAW?%s): %w", stuckIn(), ctx.Err())
	}
	select {
	case err := <-j.done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("CorelDRAW did not finish in time – it may be busy or showing a dialog; check the CorelDRAW window%s: %w", stuckIn(), ctx.Err())
	}
}

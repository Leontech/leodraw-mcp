//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package corel

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// ProcessRunning reports whether a CorelDRAW process (CorelDRW.exe) exists.
func ProcessRunning() bool { return ProcessCount() > 0 }

// ProcessCount returns the number of CorelDRW.exe processes.
func ProcessCount() int {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer syscall.CloseHandle(snap)
	n := 0
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if strings.EqualFold(syscall.UTF16ToString(e.ExeFile[:]), "CorelDRW.exe") {
			n++
		}
	}
	return n
}

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsHungAppWindow          = user32.NewProc("IsHungAppWindow")
)

// corelPIDs returns the process ids of running CorelDRW.exe processes.
func corelPIDs() map[uint32]bool {
	pids := map[uint32]bool{}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return pids
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		if strings.EqualFold(syscall.UTF16ToString(e.ExeFile[:]), "CorelDRW.exe") {
			pids[e.ProcessID] = true
		}
	}
	return pids
}

// WindowHung reports whether a visible CorelDRAW window has stopped
// processing messages ("Not responding" in Task Manager).
func WindowHung() bool {
	hungMu.Lock()
	defer hungMu.Unlock()
	hungPIDs = corelPIDs()
	if len(hungPIDs) == 0 {
		return false
	}
	hungFound = false
	hungOnce.Do(func() { hungCB = syscall.NewCallback(enumHung) }) // callbacks are a limited resource
	procEnumWindows.Call(hungCB, 0)
	return hungFound
}

var (
	hungMu    sync.Mutex
	hungOnce  sync.Once
	hungCB    uintptr
	hungPIDs  map[uint32]bool
	hungFound bool
)

func enumHung(hwnd, _ uintptr) uintptr {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if !hungPIDs[pid] {
		return 1
	}
	if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
		return 1
	}
	if h, _, _ := procIsHungAppWindow.Call(hwnd); h != 0 {
		hungFound = true
		return 0
	}
	return 1
}

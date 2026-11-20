//go:build windows

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

// Supervisor: when an MCP client starts leodraw-mcp.exe, this process only
// relays stdio to a child process (the same exe started with --child) that
// does the real work. The server_reload tool makes the child exit with
// exitReload; the supervisor then starts the exe again from disk – so an
// updated leodraw-mcp.exe is loaded without restarting the MCP client. The
// recorded initialize request is replayed to the new child and the client is
// told to refresh the tool list (notifications/tools/list_changed).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	exitReload  = 3
	reinitID    = `"__leodraw_reinit"`
	createNoWin = 0x08000000 // CREATE_NO_WINDOW
)

type supervisor struct {
	exe    string
	args   []string
	log    *log.Logger
	outMu  sync.Mutex
	out    io.Writer
	init   json.RawMessage // params of the client's initialize request
	starts []time.Time
}

func (s *supervisor) send(line []byte) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(line)
	if len(line) == 0 || line[len(line)-1] != '\n' {
		s.out.Write([]byte{'\n'})
	}
}

type childProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan int      // exit code
	reini chan struct{} // closed when the replayed initialize was answered
}

func (s *supervisor) start() (*childProc, error) {
	cmd := exec.Command(s.exe, append([]string{"--child"}, s.args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWin, HideWindow: true}
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &childProc{cmd: cmd, stdin: stdin, done: make(chan int, 1), reini: make(chan struct{})}
	go func() {
		br := bufio.NewReaderSize(stdout, 1<<20)
		reinitDone := false
		for {
			line, err := br.ReadBytes('\n')
			if t := bytes.TrimSpace(line); len(t) > 0 {
				if !reinitDone && bytes.Contains(t, []byte(reinitID)) {
					var m struct {
						ID json.RawMessage `json:"id"`
					}
					if json.Unmarshal(t, &m) == nil && string(m.ID) == reinitID {
						reinitDone = true
						close(c.reini)
						continue // swallow: the client never asked for it
					}
				}
				s.send(t)
			}
			if err != nil {
				break
			}
		}
		code := 0
		if err := cmd.Wait(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		c.done <- code
	}()
	return c, nil
}

// run relays until the client closes stdin. Returns the process exit code.
func (s *supervisor) run() int {
	client := make(chan []byte)
	go func() {
		br := bufio.NewReaderSize(os.Stdin, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if t := bytes.TrimSpace(line); len(t) > 0 {
				client <- append([]byte(nil), t...)
			}
			if err != nil {
				close(client)
				return
			}
		}
	}()

	first := true
	for {
		if !s.allowStart() {
			s.log.Printf("supervisor: child keeps crashing, giving up")
			return 1
		}
		c, err := s.start()
		if err != nil {
			s.log.Printf("supervisor: cannot start child: %v", err)
			return 1
		}
		s.log.Printf("supervisor: child started (pid %d)", c.cmd.Process.Pid)
		if !first && s.init != nil {
			if !s.replayInit(c) {
				continue // child died during re-initialization: start again
			}
			s.send([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
		}
		first = false

	relay:
		for {
			select {
			case line, ok := <-client:
				if !ok { // client gone: stop the child and exit
					c.stdin.Close()
					select {
					case <-c.done:
					case <-time.After(5 * time.Second):
						c.cmd.Process.Kill()
					}
					return 0
				}
				s.remember(line)
				if _, err := c.stdin.Write(append(line, '\n')); err != nil {
					s.log.Printf("supervisor: write to child failed: %v", err)
				}
			case code := <-c.done:
				if code == exitReload {
					s.log.Printf("supervisor: reloading server")
				} else {
					s.log.Printf("supervisor: child exited with code %d – restarting", code)
					time.Sleep(500 * time.Millisecond)
				}
				break relay
			}
		}
	}
}

// remember stores the params of the client's initialize request.
func (s *supervisor) remember(line []byte) {
	if !bytes.Contains(line, []byte(`"initialize"`)) {
		return
	}
	var m struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &m) == nil && m.Method == "initialize" {
		s.init = m.Params
	}
}

func (s *supervisor) replayInit(c *childProc) bool {
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"initialize","params":%s}`+"\n", reinitID, s.init)
	if _, err := io.WriteString(c.stdin, req); err != nil {
		return false
	}
	select {
	case <-c.reini:
	case code := <-c.done:
		c.done <- code
		return false
	case <-time.After(20 * time.Second):
		s.log.Printf("supervisor: child did not answer the replayed initialize")
	}
	io.WriteString(c.stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	return true
}

// allowStart limits restarts to 5 per minute (a crashing child must not spin).
func (s *supervisor) allowStart() bool {
	now := time.Now()
	recent := s.starts[:0]
	for _, t := range s.starts {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	s.starts = append(recent, now)
	return len(s.starts) <= 6
}

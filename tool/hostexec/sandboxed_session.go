//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

type sandboxedSession struct {
	id         string
	command    string
	proc       sandboxProcess
	stdin      io.WriteCloser
	closeIO    func() error
	cancel     context.CancelFunc
	doneCh     chan struct{}
	ioDone     chan struct{}
	ioWG       sync.WaitGroup
	mu         sync.Mutex
	started    time.Time
	finished   time.Time
	exitCode   int
	lineBase   int
	lines      []string
	partial    string
	pollCursor int
	maxLines   int
	closeOnce  sync.Once
}

func newSandboxedSession(
	id string,
	command string,
	maxLines int,
) *sandboxedSession {
	return &sandboxedSession{
		id:       id,
		command:  command,
		doneCh:   make(chan struct{}),
		ioDone:   make(chan struct{}),
		started:  time.Now(),
		maxLines: maxLines,
	}
}

func (s *sandboxedSession) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished.IsZero()
}

func (s *sandboxedSession) doneAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

func (s *sandboxedSession) markDone(exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished.IsZero() {
		return
	}
	if s.partial != "" {
		s.lines = append(s.lines, s.partial)
		s.partial = ""
	}
	s.exitCode = exitCode
	s.finished = time.Now()
	close(s.doneCh)
}

func (s *sandboxedSession) readFrom(reader io.Reader) {
	if reader == nil {
		return
	}
	bufReader := bufio.NewReaderSize(reader, 32*1024)
	for {
		chunk, err := bufReader.ReadBytes('\n')
		if len(chunk) > 0 {
			s.appendOutput(string(chunk))
		}
		if err != nil {
			return
		}
	}
}

func (s *sandboxedSession) appendOutput(chunk string) {
	text := strings.ReplaceAll(chunk, "\r\n", "\n")
	s.mu.Lock()
	defer s.mu.Unlock()
	text = s.partial + text
	parts := strings.Split(text, "\n")
	if len(parts) == 0 {
		return
	}
	s.partial = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		s.lines = append(s.lines, line)
	}
	s.trimLocked()
}

func (s *sandboxedSession) trimLocked() {
	if s.maxLines <= 0 {
		return
	}
	if len(s.lines) <= s.maxLines {
		return
	}
	drop := len(s.lines) - s.maxLines
	s.lines = s.lines[drop:]
	s.lineBase += drop
	if s.pollCursor < s.lineBase {
		s.pollCursor = s.lineBase
	}
}

func (s *sandboxedSession) pollTail(lines int) string {
	poll := s.poll(nil)
	return trimOutputTail(poll.Output, lines)
}

func (s *sandboxedSession) allOutput() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := strings.Join(s.lines, "\n")
	if s.partial != "" {
		if out != "" {
			out += "\n"
		}
		out += s.partial
	}
	return out, s.exitCode
}

func (s *sandboxedSession) poll(limit *int) processPoll {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := s.pollCursor
	if start < s.lineBase {
		start = s.lineBase
		s.pollCursor = start
	}
	end := s.lineBase + len(s.lines)
	if limit != nil && *limit > 0 {
		if want := start + *limit; want < end {
			end = want
		}
	}
	from := start - s.lineBase
	to := end - s.lineBase
	out := strings.Join(s.lines[from:to], "\n")
	if end == s.lineBase+len(s.lines) && s.partial != "" {
		if out != "" {
			out += "\n"
		}
		out += s.partial
	}
	s.pollCursor = end
	res := processPoll{
		Status:     programStatusRunning,
		Output:     out,
		Offset:     start,
		NextOffset: end,
	}
	if s.finished.IsZero() {
		return res
	}
	res.Status = programStatusExited
	res.ExitCode = intPtr(s.exitCode)
	return res
}

func (s *sandboxedSession) write(data string, newline bool) error {
	if data == "" && !newline {
		return nil
	}
	s.mu.Lock()
	stdin := s.stdin
	running := s.finished.IsZero()
	s.mu.Unlock()
	if !running {
		return errors.New("session is not running")
	}
	if stdin == nil {
		return errors.New("stdin is not available")
	}
	text := data
	if newline {
		text += "\n"
	}
	_, err := io.WriteString(stdin, text)
	return err
}

func (s *sandboxedSession) kill(
	ctx context.Context,
	grace time.Duration,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	proc := s.proc
	cancel := s.cancel
	s.mu.Unlock()
	if proc == nil {
		if cancel != nil {
			cancel()
		}
		return nil
	}
	_ = proc.SignalTerminate()
	if grace < 0 {
		grace = 0
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-s.doneCh:
		if cancel != nil {
			cancel()
		}
		return nil
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		return proc.ForceKill()
	case <-timer.C:
		if cancel != nil {
			cancel()
		}
		return proc.ForceKill()
	}
}

func (s *sandboxedSession) close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.closeIO != nil {
			err = s.closeIO()
		}
	})
	return err
}

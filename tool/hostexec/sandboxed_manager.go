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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type sandboxedManager struct {
	mu       sync.Mutex
	sessions map[string]*sandboxedSession
	maxLines int
	jobTTL   time.Duration
	cfg      resolvedSandboxedConfig
	runner   sandboxRunner
	clock    func() time.Time
}

func newSandboxedManager(
	cfg resolvedSandboxedConfig,
) (*sandboxedManager, error) {
	runner, err := newSandboxRunner(cfg)
	if err != nil {
		return nil, err
	}
	return &sandboxedManager{
		sessions: map[string]*sandboxedSession{},
		maxLines: cfg.maxLines,
		jobTTL:   cfg.jobTTL,
		cfg:      cfg,
		runner:   runner,
		clock:    time.Now,
	}, nil
}

func (m *sandboxedManager) exec(
	ctx context.Context,
	params execParams,
) (execResult, error) {
	if ctx == nil {
		return execResult{}, errors.New("nil context")
	}
	if strings.TrimSpace(params.Command) == "" {
		return execResult{}, errors.New(errCommandRequired)
	}
	if params.Pty {
		return execResult{}, errSandboxTTYUnsupported
	}
	m.cleanupExpired()
	yieldMs := defaultYieldMS
	if params.YieldMs != nil && *params.YieldMs >= 0 {
		yieldMs = *params.YieldMs
	}
	timeout, err := m.resolveTimeout(params.TimeoutS)
	if err != nil {
		return execResult{}, err
	}
	env, err := m.resolveEnv(params.Env)
	if err != nil {
		return execResult{}, err
	}
	sess, err := m.startBackground(params.Command, params.Workdir, env, timeout)
	if err != nil {
		return execResult{}, err
	}
	if params.Background {
		return execResult{
			Status:    programStatusRunning,
			SessionID: sess.id,
			Output:    sess.pollTail(defaultLogTail),
		}, nil
	}
	if yieldMs == 0 {
		select {
		case <-ctx.Done():
			_ = m.kill(sess.id)
			return execResult{}, ctx.Err()
		case <-sess.doneCh:
		}
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		return execResult{
			Status:   programStatusExited,
			Output:   out,
			ExitCode: intPtr(code),
		}, nil
	}
	timer := time.NewTimer(time.Duration(yieldMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = m.kill(sess.id)
		return execResult{}, ctx.Err()
	case <-sess.doneCh:
		out, code := sess.allOutput()
		_ = m.clearFinished(sess.id)
		return execResult{
			Status:   programStatusExited,
			Output:   out,
			ExitCode: intPtr(code),
		}, nil
	case <-timer.C:
		return execResult{
			Status:    programStatusRunning,
			SessionID: sess.id,
			Output:    sess.pollTail(defaultLogTail),
		}, nil
	}
}

func (m *sandboxedManager) resolveTimeout(
	raw *int,
) (time.Duration, error) {
	timeout := m.cfg.maxTimeout
	if raw == nil || *raw <= 0 {
		return timeout, nil
	}
	requested := timeoutDuration(*raw)
	if requested > m.cfg.maxTimeout {
		return 0, fmt.Errorf("timeout exceeds sandbox max timeout: %s", m.cfg.maxTimeout)
	}
	return requested, nil
}

func (m *sandboxedManager) resolveEnv(
	extra map[string]string,
) (map[string]string, error) {
	if err := validateSandboxEnvMap(extra, m.cfg.allowedEnvSet); err != nil {
		return nil, err
	}
	return mergeSandboxEnv(m.cfg, extra), nil
}

func (m *sandboxedManager) startBackground(
	command string,
	workdir string,
	env map[string]string,
	timeout time.Duration,
) (*sandboxedSession, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), timeout)
	proc, err := m.runner.Start(runCtx, sandboxStartRequest{
		Command: command,
		Workdir: workdir,
		Env:     env,
		Timeout: timeout,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	sess := newSandboxedSession(newSessionID(), command, m.maxLines)
	sess.cancel = cancel
	sess.proc = proc
	sess.stdin = proc.Stdin()
	sess.closeIO = proc.Close
	sess.ioWG.Add(2)
	go func() {
		defer sess.ioWG.Done()
		sess.readFrom(proc.Stdout())
	}()
	go func() {
		defer sess.ioWG.Done()
		sess.readFrom(proc.Stderr())
	}()
	go func() {
		sess.ioWG.Wait()
		close(sess.ioDone)
	}()
	m.mu.Lock()
	m.sessions[sess.id] = sess
	m.mu.Unlock()
	go func(sessionID string) {
		<-runCtx.Done()
		_ = m.kill(sessionID)
	}(sess.id)
	go func() {
		processState, _ := proc.Wait()
		waitDone(sess.ioDone, defaultIODrain)
		code := -1
		if processState != nil {
			code = processState.ExitCode()
		}
		sess.markDone(code)
		cancel()
		_ = proc.Close()
	}()
	return sess, nil
}

func (m *sandboxedManager) poll(
	id string,
	limit *int,
) (processPoll, error) {
	sess, err := m.get(id)
	if err != nil {
		return processPoll{}, err
	}
	return sess.poll(limit), nil
}

func (m *sandboxedManager) write(
	id string,
	data string,
	newline bool,
) error {
	sess, err := m.get(id)
	if err != nil {
		return err
	}
	return sess.write(data, newline)
}

func (m *sandboxedManager) kill(id string) error {
	return m.killContext(context.Background(), id)
}

func (m *sandboxedManager) killContext(
	ctx context.Context,
	id string,
) error {
	sess, err := m.get(id)
	if err != nil {
		return err
	}
	return sess.kill(ctx, defaultKillGrace)
}

func (m *sandboxedManager) clearFinished(id string) error {
	sess, err := m.get(id)
	if err != nil {
		return err
	}
	if sess.running() {
		return errors.New("session is still running")
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func (m *sandboxedManager) get(id string) (*sandboxedSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errUnknownSession, id)
	}
	return sess, nil
}

func (m *sandboxedManager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	for id, sess := range m.sessions {
		if sess.running() {
			continue
		}
		if now.Sub(sess.doneAt()) < m.jobTTL {
			continue
		}
		delete(m.sessions, id)
	}
}

func (m *sandboxedManager) close() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		if err := m.kill(id); err != nil && !errors.Is(err, errUnknownSession) && firstErr == nil {
			firstErr = err
		}
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
	}
	return firstErr
}

func validateSandboxEnvMap(
	env map[string]string,
	allowed map[string]struct{},
) error {
	for key := range env {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, fixed := sandboxFixedEnvKeys[trimmed]; fixed {
			return fmt.Errorf("sandboxed hostexec does not allow overriding env key: %s", trimmed)
		}
		if _, ok := allowed[trimmed]; !ok {
			return fmt.Errorf("sandboxed hostexec does not allow env key: %s", trimmed)
		}
	}
	return nil
}

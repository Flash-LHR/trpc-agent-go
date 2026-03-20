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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NewSandboxedToolSet creates a Linux-only sandboxed hostexec tool set.
func NewSandboxedToolSet(cfg SandboxedConfig) (tool.ToolSet, error) {
	resolved, err := resolveSandboxedConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := probeSandboxReadiness(resolved); err != nil {
		return nil, err
	}
	mgr, err := newSandboxedManager(resolved)
	if err != nil {
		return nil, err
	}
	set := &sandboxedToolSet{
		name:        resolved.name,
		baseDir:     resolved.baseDir,
		baseDirReal: resolved.baseDirReal,
		mgr:         mgr,
	}
	set.tools = []tool.Tool{
		&sandboxedExecCommandTool{
			mgr:         mgr,
			baseDir:     resolved.baseDir,
			baseDirReal: resolved.baseDirReal,
		},
		&sandboxedWriteStdinTool{mgr: mgr},
		&sandboxedKillSessionTool{mgr: mgr},
	}
	return set, nil
}

type sandboxedToolSet struct {
	name        string
	baseDir     string
	baseDirReal string
	mgr         *sandboxedManager
	tools       []tool.Tool
}

func (s *sandboxedToolSet) Tools(context.Context) []tool.Tool {
	return s.tools
}

func (s *sandboxedToolSet) Close() error {
	if s == nil || s.mgr == nil {
		return nil
	}
	return s.mgr.close()
}

func (s *sandboxedToolSet) Name() string {
	return s.name
}

type sandboxedExecCommandTool struct {
	mgr         *sandboxedManager
	baseDir     string
	baseDirReal string
}

func (t *sandboxedExecCommandTool) Declaration() *tool.Declaration {
	return (&execCommandTool{}).Declaration()
}

func (t *sandboxedExecCommandTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errExecToolNotConfigured)
	}
	var in execInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New(errCommandRequired)
	}
	workdir, err := resolveSandboxWorkdir(in.Workdir, t.baseDir, t.baseDirReal)
	if err != nil {
		return nil, err
	}
	yield := firstInt(in.YieldTimeMS, in.YieldMs)
	timeout := firstInt(in.TimeoutSec, in.TimeoutSecOld)
	res, err := t.mgr.exec(ctx, execParams{
		Command:    in.Command,
		Workdir:    workdir,
		Env:        in.Env,
		Pty:        firstBool(in.TTY, in.PTY),
		Background: in.Background,
		YieldMs:    yield,
		TimeoutS:   timeout,
	})
	if err != nil {
		return nil, err
	}
	return mapExecResult(res), nil
}

type sandboxedWriteStdinTool struct {
	mgr *sandboxedManager
}

func (t *sandboxedWriteStdinTool) Declaration() *tool.Declaration {
	return (&writeStdinTool{}).Declaration()
}

func (t *sandboxedWriteStdinTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errWriteToolNotConfigured)
	}
	var in writeInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDOld)
	if sessionID == "" {
		return nil, errors.New(errSessionIDRequired)
	}
	if err := t.mgr.write(sessionID, in.Chars, firstBool(in.AppendNewline, in.Submit)); err != nil {
		return nil, err
	}
	yield := defaultWriteYieldMS
	if v := firstInt(in.YieldTimeMS, in.YieldMs); v != nil && *v >= 0 {
		yield = *v
	}
	if yield > 0 {
		timer := time.NewTimer(time.Duration(yield) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	poll, err := t.mgr.poll(sessionID, nil)
	if err != nil {
		return nil, err
	}
	return mapPollResult(sessionID, poll), nil
}

type sandboxedKillSessionTool struct {
	mgr *sandboxedManager
}

func (t *sandboxedKillSessionTool) Declaration() *tool.Declaration {
	return (&killSessionTool{}).Declaration()
}

func (t *sandboxedKillSessionTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errKillToolNotConfigured)
	}
	var in killInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDOld)
	if sessionID == "" {
		return nil, errors.New(errSessionIDRequired)
	}
	err := t.mgr.killContext(ctx, sessionID)
	return map[string]any{
		"ok":         err == nil,
		"session_id": sessionID,
	}, err
}

var _ tool.ToolSet = (*sandboxedToolSet)(nil)
var _ tool.CallableTool = (*sandboxedExecCommandTool)(nil)
var _ tool.CallableTool = (*sandboxedWriteStdinTool)(nil)
var _ tool.CallableTool = (*sandboxedKillSessionTool)(nil)

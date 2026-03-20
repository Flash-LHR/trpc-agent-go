//go:build linux

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
	"io"
	"os"
	"os/exec"
	"slices"
	"syscall"
)

type bwrapSandboxRunner struct {
	cfg resolvedSandboxedConfig
}

func newSandboxRunner(
	cfg resolvedSandboxedConfig,
) (sandboxRunner, error) {
	return &bwrapSandboxRunner{cfg: cfg}, nil
}

func (r *bwrapSandboxRunner) Start(
	_ context.Context,
	req sandboxStartRequest,
) (sandboxProcess, error) {
	args := buildBwrapArgs(r.cfg, req)
	// nosemgrep: go.lang.security.audit.dangerous-exec-command
	// The sandbox runner intentionally executes the configured launcher.
	cmd := exec.Command(r.cfg.bwrapPath, args...) //nolint:gosec
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, stdout, stderr, err := startPipes(cmd)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	return &bwrapSandboxProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}, nil
}

func buildBwrapArgs(
	cfg resolvedSandboxedConfig,
	req sandboxStartRequest,
) []string {
	workdir := req.Workdir
	if workdir == "" {
		workdir = cfg.baseDirReal
	}
	args := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-net",
		"--clearenv",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}
	for _, path := range cfg.systemBinds {
		args = append(args, "--ro-bind", path, path)
	}
	args = append(args, "--ro-bind", cfg.baseDirReal, cfg.baseDirReal)
	for _, path := range cfg.readOnlyPaths {
		args = append(args, "--ro-bind", path, path)
	}
	for _, path := range cfg.writablePaths {
		args = append(args, "--bind", path, path)
	}
	keys := make([]string, 0, len(req.Env))
	for key := range req.Env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		args = append(args, "--setenv", key, req.Env[key])
	}
	args = append(args, "--setenv", "PWD", workdir, "--chdir", workdir, "--", cfg.shellPath)
	args = append(args, cfg.shellArgs...)
	args = append(args, req.Command)
	return args
}

type bwrapSandboxProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (p *bwrapSandboxProcess) Stdin() io.WriteCloser {
	return p.stdin
}

func (p *bwrapSandboxProcess) Stdout() io.ReadCloser {
	return p.stdout
}

func (p *bwrapSandboxProcess) Stderr() io.ReadCloser {
	return p.stderr
}

func (p *bwrapSandboxProcess) Wait() (*os.ProcessState, error) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil, nil
	}
	return p.cmd.Process.Wait()
}

func (p *bwrapSandboxProcess) SignalTerminate() error {
	return killSandboxProcessGroup(p.cmd, syscall.SIGTERM)
}

func (p *bwrapSandboxProcess) ForceKill() error {
	return killSandboxProcessGroup(p.cmd, syscall.SIGKILL)
}

func (p *bwrapSandboxProcess) Close() error {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	if p.stderr != nil {
		_ = p.stderr.Close()
	}
	return nil
}

func killSandboxProcessGroup(
	cmd *exec.Cmd,
	sig syscall.Signal,
) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		if err := syscall.Kill(-pgid, sig); err == nil || err == syscall.ESRCH {
			return nil
		}
	}
	err = cmd.Process.Signal(sig)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestResolveSandboxWorkdir(t *testing.T) {
	baseDir := t.TempDir()
	baseDirReal, err := realpathDir(baseDir)
	require.NoError(t, err)
	subDir := filepath.Join(baseDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	workdir, err := resolveSandboxWorkdir("sub", baseDir, baseDirReal)
	require.NoError(t, err)
	require.Equal(t, subDir, workdir)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	homeReal, err := realpathDir(home)
	require.NoError(t, err)
	workdir, err = resolveSandboxWorkdir("~", home, homeReal)
	require.NoError(t, err)
	require.Equal(t, homeReal, workdir)
	outside := t.TempDir()
	link := filepath.Join(baseDir, "escape")
	require.NoError(t, os.Symlink(outside, link))
	_, err = resolveSandboxWorkdir("escape", baseDir, baseDirReal)
	require.ErrorContains(t, err, "sandbox workdir escapes base dir")
}

func TestNewSandboxedToolSet_ConfigValidation(t *testing.T) {
	baseDir := t.TempDir()
	shellPath, _, err := shellSpec()
	require.NoError(t, err)
	binDir := t.TempDir()
	require.NoError(t, os.Symlink(shellPath, filepath.Join(binDir, "bash")))
	t.Setenv("PATH", binDir)
	_, err = NewSandboxedToolSet(SandboxedConfig{
		BaseDir: baseDir,
	})
	require.Error(t, err)
	installFakeBwrap(t)
	_, err = NewSandboxedToolSet(SandboxedConfig{
		BaseDir:        baseDir,
		AllowedEnvKeys: []string{"PATH"},
		BaseEnv: map[string]string{
			"HOSTEXEC_SECRET": "x",
		},
	})
	require.ErrorContains(t, err, "does not allow env key")
	_, err = NewSandboxedToolSet(SandboxedConfig{
		BaseDir:       baseDir,
		WritablePaths: []string{t.TempDir()},
	})
	require.ErrorContains(t, err, "bind path escapes base dir")
}

func TestNewSandboxedToolSet_ForegroundRelativeWorkdir(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	baseDir := t.TempDir()
	subDir := filepath.Join(baseDir, "sub")
	outDir := filepath.Join(baseDir, "out")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "note.txt"), []byte("sandbox"), 0o644))
	installFakeBwrap(t)
	set, err := NewSandboxedToolSet(SandboxedConfig{
		BaseDir:       baseDir,
		WritablePaths: []string{"out"},
	})
	require.NoError(t, err)
	defer set.Close()
	execTool, _, _, _ := sandboxToolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "cat note.txt; printf done > ../out/result.txt",
			"workdir": "sub",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)
	res := out.(map[string]any)
	require.Equal(t, programStatusExited, res["status"])
	require.Contains(t, outputField(res), "sandbox")
	data, err := os.ReadFile(filepath.Join(outDir, "result.txt"))
	require.NoError(t, err)
	require.Equal(t, "done", string(data))
}

func TestNewSandboxedToolSet_HostEnvIsolation(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	t.Setenv("HOSTEXEC_SECRET", "from-host")
	installFakeBwrap(t)
	baseDir := t.TempDir()
	set, err := NewSandboxedToolSet(SandboxedConfig{
		BaseDir:        baseDir,
		AllowedEnvKeys: []string{"PATH", "HOSTEXEC_SECRET"},
	})
	require.NoError(t, err)
	defer set.Close()
	execTool, _, _, _ := sandboxToolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "printf '%s' \"${HOSTEXEC_SECRET:-missing}\"",
			"yieldMs": 0,
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "missing", strings.TrimSpace(outputField(out.(map[string]any))))
}

func TestNewSandboxedToolSet_RejectsFixedEnvOverride(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	baseDir := t.TempDir()
	installFakeBwrap(t)
	set, err := NewSandboxedToolSet(SandboxedConfig{
		BaseDir: baseDir,
	})
	require.NoError(t, err)
	defer set.Close()
	execTool, _, _, _ := sandboxToolSetTools(t, set)
	_, err = execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo hi",
			"env": map[string]string{
				"HOME": "/tmp/custom-home",
			},
		}),
	)
	require.ErrorContains(t, err, "does not allow overriding env key: HOME")
}

func TestNewSandboxedToolSet_WriteAliases(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	baseDir := t.TempDir()
	installFakeBwrap(t)
	set, err := NewSandboxedToolSet(SandboxedConfig{
		BaseDir: baseDir,
	})
	require.NoError(t, err)
	defer set.Close()
	execTool, writeTool, _, mgr := sandboxToolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command":    "read -r line; echo got:$line",
			"background": true,
			"timeoutSec": 5,
		}),
	)
	require.NoError(t, err)
	sessionID := out.(map[string]any)["session_id"].(string)
	writeOut, err := writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"sessionId": sessionID,
			"chars":     "hi",
			"submit":    true,
		}),
	)
	require.NoError(t, err)
	all := outputField(writeOut.(map[string]any))
	all += sandboxPollUntilExited(t, mgr, sessionID)
	require.Contains(t, all, "got:hi")
}

func TestNewSandboxedToolSet_PTYRejected(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	baseDir := t.TempDir()
	installFakeBwrap(t)
	set, err := NewSandboxedToolSet(SandboxedConfig{
		BaseDir: baseDir,
	})
	require.NoError(t, err)
	defer set.Close()
	execTool, _, _, _ := sandboxToolSetTools(t, set)
	_, err = execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "echo hi",
			"pty":     true,
		}),
	)
	require.ErrorIs(t, err, errSandboxTTYUnsupported)
}

func TestNewSandboxedToolSet_KillSessionKillsProcessGroup(t *testing.T) {
	if _, _, err := shellSpec(); err != nil {
		t.Skip(err.Error())
	}
	baseDir := t.TempDir()
	installFakeBwrap(t)
	set, err := NewSandboxedToolSet(SandboxedConfig{
		BaseDir: baseDir,
	})
	require.NoError(t, err)
	defer set.Close()
	execTool, _, killTool, mgr := sandboxToolSetTools(t, set)
	out, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"command": "sleep 30 & child=$!; echo $child; wait",
			"yieldMs": 50,
		}),
	)
	require.NoError(t, err)
	res := out.(map[string]any)
	require.Equal(t, programStatusRunning, res["status"])
	sessionID := res["session_id"].(string)
	output := outputField(res)
	if firstPID(output) == 0 {
		output += waitForSandboxOutputContains(t, mgr, sessionID, "")
	}
	childPID := firstPID(output)
	require.Positive(t, childPID)
	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"session_id": sessionID,
		}),
	)
	require.NoError(t, err)
	_ = sandboxPollUntilExited(t, mgr, sessionID)
	require.Eventually(t, func() bool {
		return syscall.Kill(childPID, 0) == syscall.ESRCH
	}, 3*time.Second, 50*time.Millisecond)
}

func TestSandboxConfigHelpers(t *testing.T) {
	allowed := buildAllowedEnvSet([]string{"LANG", "HOSTEXEC_ALLOWED"})
	require.Contains(t, allowed, "PATH")
	require.Contains(t, allowed, "HOME")
	require.Contains(t, allowed, "TMPDIR")
	filtered := filterSandboxEnvMap(
		map[string]string{
			"HOSTEXEC_ALLOWED": "1",
			"HOME":             "/tmp/custom-home",
			" ":                "skip",
		},
		allowed,
	)
	require.Equal(t, map[string]string{"HOSTEXEC_ALLOWED": "1"}, filtered)
	merged := mergeSandboxEnv(
		resolvedSandboxedConfig{
			baselineEnv: map[string]string{
				"PATH":   "/usr/bin:/bin",
				"HOME":   "/tmp",
				"TMPDIR": "/tmp",
			},
			baseEnv:       map[string]string{"LANG": "C"},
			allowedEnvSet: allowed,
		},
		map[string]string{
			"HOSTEXEC_ALLOWED": "2",
			"HOME":             "/tmp/ignored",
		},
	)
	require.Equal(t, "/tmp", merged["HOME"])
	require.Equal(t, "C", merged["LANG"])
	require.Equal(t, "2", merged["HOSTEXEC_ALLOWED"])
	require.True(t, pathsOverlap("/tmp/base", "/tmp/base/sub"))
	require.False(t, pathsOverlap("/tmp/base", "/tmp/elsewhere"))
	require.NoError(t, probeSandboxReadiness(resolvedSandboxedConfig{
		bwrapPath:   "/usr/bin/bwrap",
		baseDirReal: "/tmp",
		shellPath:   "/bin/sh",
	}))
	require.ErrorContains(t, probeSandboxReadiness(resolvedSandboxedConfig{}), "sandbox bwrap path is empty")
	filePath := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o644))
	_, err := realpathDir(filePath)
	require.ErrorContains(t, err, "path is not a directory")
}

func TestSandboxToolSet_HelperBranches(t *testing.T) {
	var nilSet *sandboxedToolSet
	require.NoError(t, nilSet.Close())
	set := &sandboxedToolSet{name: "sandbox-tool"}
	require.Equal(t, "sandbox-tool", set.Name())
	require.Nil(t, set.Tools(context.Background()))
	var execTool *sandboxedExecCommandTool
	require.Equal(t, toolExecCommand, execTool.Declaration().Name)
	_, err := execTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"command": "echo hi"}),
	)
	require.EqualError(t, err, errExecToolNotConfigured)
	var writeTool *sandboxedWriteStdinTool
	require.Equal(t, toolWriteStdin, writeTool.Declaration().Name)
	_, err = writeTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"session_id": "x"}),
	)
	require.EqualError(t, err, errWriteToolNotConfigured)
	var killTool *sandboxedKillSessionTool
	require.Equal(t, toolKillSession, killTool.Declaration().Name)
	_, err = killTool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"session_id": "x"}),
	)
	require.EqualError(t, err, errKillToolNotConfigured)
}

func TestBwrapSandboxProcess_HelperBranches(t *testing.T) {
	proc := &bwrapSandboxProcess{}
	state, err := proc.Wait()
	require.NoError(t, err)
	require.Nil(t, state)
	require.NoError(t, proc.SignalTerminate())
	require.NoError(t, proc.ForceKill())
	require.NoError(t, proc.Close())
}

func TestSandboxedSession_HelperBranches(t *testing.T) {
	sess := newSandboxedSession("s", "cmd", 1)
	require.True(t, sess.doneAt().IsZero())
	sess.appendOutput("first\nsecond\n")
	require.Equal(t, 1, sess.lineBase)
	require.Equal(t, []string{"second"}, sess.lines)
	sess.partial = "tail"
	sess.markDone(7)
	require.False(t, sess.doneAt().IsZero())
	out, code := sess.allOutput()
	require.Equal(t, "second\ntail", out)
	require.Equal(t, 7, code)
	canceled := false
	idle := newSandboxedSession("idle", "cmd", defaultMaxLines)
	idle.cancel = func() { canceled = true }
	require.NoError(t, idle.kill(context.Background(), -1))
	require.True(t, canceled)
	closeErr := errors.New("close failed")
	closeCount := 0
	closer := newSandboxedSession("close", "cmd", defaultMaxLines)
	closer.closeIO = func() error {
		closeCount++
		return closeErr
	}
	require.ErrorIs(t, closer.close(), closeErr)
	require.NoError(t, closer.close())
	require.Equal(t, 1, closeCount)
	force := &stubSandboxProcess{}
	forced := newSandboxedSession("force", "cmd", defaultMaxLines)
	forced.proc = force
	require.NoError(t, forced.kill(context.Background(), 0))
	require.True(t, force.forceKillCalled)
}

func sandboxToolSetTools(
	t *testing.T,
	set tool.ToolSet,
) (
	tool.CallableTool,
	tool.CallableTool,
	tool.CallableTool,
	*sandboxedManager,
) {
	t.Helper()
	typed, ok := set.(*sandboxedToolSet)
	require.True(t, ok)
	tools := typed.Tools(context.Background())
	require.Len(t, tools, 3)
	return tools[0].(tool.CallableTool),
		tools[1].(tool.CallableTool),
		tools[2].(tool.CallableTool),
		typed.mgr
}

func sandboxPollUntilExited(
	t *testing.T,
	mgr *sandboxedManager,
	sessionID string,
) string {
	t.Helper()
	var out strings.Builder
	var pollErr error
	require.Eventually(t, func() bool {
		poll, err := mgr.poll(sessionID, nil)
		if err != nil {
			pollErr = err
			return false
		}
		if poll.Output != "" {
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString(poll.Output)
		}
		return poll.Status == programStatusExited
	}, 3*time.Second, 50*time.Millisecond)
	require.NoError(t, pollErr)
	return out.String()
}

func waitForSandboxOutputContains(
	t *testing.T,
	mgr *sandboxedManager,
	sessionID string,
	substr string,
) string {
	t.Helper()
	var out strings.Builder
	var pollErr error
	require.Eventually(t, func() bool {
		poll, err := mgr.poll(sessionID, nil)
		if err != nil {
			pollErr = err
			return false
		}
		if poll.Output != "" {
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString(poll.Output)
		}
		if substr == "" {
			return strings.TrimSpace(out.String()) != ""
		}
		return strings.Contains(out.String(), substr)
	}, 3*time.Second, 50*time.Millisecond)
	require.NoError(t, pollErr)
	return out.String()
}

func writeFakeBwrap(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bwrap")
	script := `#!/bin/sh
set -eu
workdir=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--die-with-parent|--unshare-pid|--unshare-net|--clearenv)
		shift
		;;
	--proc|--dev|--tmpfs)
		shift 2
		;;
	--ro-bind|--bind)
		shift 3
		;;
	--setenv)
		key="$2"
		value="$3"
		export "$key=$value"
		shift 3
		;;
	--chdir)
		workdir="$2"
		shift 2
		;;
	--)
		shift
		break
		;;
	*)
		echo "unexpected arg: $1" >&2
		exit 97
		;;
	esac
done
if [ -n "$workdir" ]; then
	cd "$workdir"
fi
exec "$@"
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func installFakeBwrap(t *testing.T) {
	t.Helper()
	path := writeFakeBwrap(t)
	currentPath := os.Getenv("PATH")
	t.Setenv("PATH", filepath.Dir(path)+string(os.PathListSeparator)+currentPath)
}

func firstPID(output string) int {
	for _, field := range strings.Fields(output) {
		pid, err := strconv.Atoi(strings.TrimSpace(field))
		if err == nil && pid > 0 {
			return pid
		}
	}
	return 0
}

type stubSandboxProcess struct {
	forceKillCalled bool
}

func (s *stubSandboxProcess) Stdin() io.WriteCloser {
	return nil
}

func (s *stubSandboxProcess) Stdout() io.ReadCloser {
	return nil
}

func (s *stubSandboxProcess) Stderr() io.ReadCloser {
	return nil
}

func (s *stubSandboxProcess) Wait() (*os.ProcessState, error) {
	return nil, nil
}

func (s *stubSandboxProcess) SignalTerminate() error {
	return nil
}

func (s *stubSandboxProcess) ForceKill() error {
	s.forceKillCalled = true
	return nil
}

func (s *stubSandboxProcess) Close() error {
	return nil
}

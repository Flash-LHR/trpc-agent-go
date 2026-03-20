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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const defaultSandboxMaxTimeout = 30 * time.Minute

var (
	errSandboxUnsupported     = errors.New("sandboxed hostexec is only supported on linux")
	errSandboxBaseDirRequired = errors.New("sandboxed hostexec requires base dir")
	errSandboxTTYUnsupported  = errors.New("sandboxed hostexec does not support tty")
)

// SandboxedConfig configures the Linux sandboxed hostexec tool set.
type SandboxedConfig struct {
	Name           string
	BaseDir        string
	MaxLines       int
	JobTTL         time.Duration
	BaseEnv        map[string]string
	MaxTimeout     time.Duration
	WritablePaths  []string
	ReadOnlyPaths  []string
	AllowedEnvKeys []string
}

type resolvedSandboxedConfig struct {
	name          string
	baseDir       string
	baseDirReal   string
	bwrapPath     string
	shellPath     string
	shellArgs     []string
	maxLines      int
	jobTTL        time.Duration
	maxTimeout    time.Duration
	baseEnv       map[string]string
	baselineEnv   map[string]string
	allowedEnvSet map[string]struct{}
	writablePaths []string
	readOnlyPaths []string
	systemBinds   []string
}

var defaultSandboxEnvKeys = []string{
	"HOME",
	"LANG",
	"LC_ALL",
	"PATH",
	"TERM",
	"TMPDIR",
}

var sandboxFixedEnvKeys = map[string]string{
	"HOME":   "/tmp",
	"TMPDIR": "/tmp",
}

var defaultSandboxSystemPaths = []string{
	"/bin",
	"/sbin",
	"/usr",
	"/lib",
	"/lib64",
	"/etc",
	"/usr/local",
	"/opt",
	"/nix/store",
	"/run/current-system/sw",
}

func resolveSandboxedConfig(
	cfg SandboxedConfig,
) (resolvedSandboxedConfig, error) {
	if runtime.GOOS != "linux" {
		return resolvedSandboxedConfig{}, errSandboxUnsupported
	}
	baseDir, err := resolveBaseDir(cfg.BaseDir)
	if err != nil {
		return resolvedSandboxedConfig{}, err
	}
	if strings.TrimSpace(baseDir) == "" {
		return resolvedSandboxedConfig{}, errSandboxBaseDirRequired
	}
	baseDirReal, err := realpathDir(baseDir)
	if err != nil {
		return resolvedSandboxedConfig{}, err
	}
	bwrapPath, err := resolveBwrapPath()
	if err != nil {
		return resolvedSandboxedConfig{}, err
	}
	shellPath, shellArgs, err := shellSpec()
	if err != nil {
		return resolvedSandboxedConfig{}, err
	}
	shellReal, err := filepath.EvalSymlinks(shellPath)
	if err != nil {
		return resolvedSandboxedConfig{}, fmt.Errorf("resolve shell path: %w", err)
	}
	systemBinds := existingSandboxSystemPaths()
	if !pathHasSystemBind(shellReal, systemBinds) {
		return resolvedSandboxedConfig{}, fmt.Errorf("unsupported shell path for sandbox: %s", shellReal)
	}
	allowedEnvSet := buildAllowedEnvSet(cfg.AllowedEnvKeys)
	baselineEnv := collectSandboxBaselineEnv(allowedEnvSet)
	if err := validateSandboxEnvMap(cfg.BaseEnv, allowedEnvSet); err != nil {
		return resolvedSandboxedConfig{}, err
	}
	baseEnv := filterSandboxEnvMap(cfg.BaseEnv, allowedEnvSet)
	writablePaths, err := resolveSandboxBindPaths(cfg.WritablePaths, baseDir, baseDirReal)
	if err != nil {
		return resolvedSandboxedConfig{}, err
	}
	readOnlyPaths, err := resolveSandboxBindPaths(cfg.ReadOnlyPaths, baseDir, baseDirReal)
	if err != nil {
		return resolvedSandboxedConfig{}, err
	}
	if err := validateSandboxBindOverlap(writablePaths, readOnlyPaths); err != nil {
		return resolvedSandboxedConfig{}, err
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = defaultToolSetName + "_sandboxed"
	}
	maxLines := defaultMaxLines
	if cfg.MaxLines > 0 {
		maxLines = cfg.MaxLines
	}
	jobTTL := defaultJobTTL
	if cfg.JobTTL > 0 {
		jobTTL = cfg.JobTTL
	}
	maxTimeout := defaultSandboxMaxTimeout
	if cfg.MaxTimeout > 0 {
		maxTimeout = cfg.MaxTimeout
	}
	return resolvedSandboxedConfig{
		name:          name,
		baseDir:       baseDir,
		baseDirReal:   baseDirReal,
		bwrapPath:     bwrapPath,
		shellPath:     shellReal,
		shellArgs:     append([]string(nil), shellArgs...),
		maxLines:      maxLines,
		jobTTL:        jobTTL,
		maxTimeout:    maxTimeout,
		baseEnv:       baseEnv,
		baselineEnv:   baselineEnv,
		allowedEnvSet: allowedEnvSet,
		writablePaths: writablePaths,
		readOnlyPaths: readOnlyPaths,
		systemBinds:   systemBinds,
	}, nil
}

func resolveBwrapPath() (string, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return "", fmt.Errorf("find bwrap: %w", err)
	}
	return filepath.Abs(path)
}

func realpathDir(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", real)
	}
	return real, nil
}

func resolveSandboxBindPaths(
	rawPaths []string,
	baseDir string,
	baseDirReal string,
) ([]string, error) {
	if len(rawPaths) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(rawPaths))
	for _, raw := range rawPaths {
		resolved, err := resolveWorkdir(raw, baseDir)
		if err != nil {
			return nil, err
		}
		real, err := filepath.EvalSymlinks(resolved)
		if err != nil {
			return nil, fmt.Errorf("resolve bind path %q: %w", raw, err)
		}
		if !pathWithinBase(baseDirReal, real) {
			return nil, fmt.Errorf("bind path escapes base dir: %s", real)
		}
		if _, err := os.Stat(real); err != nil {
			return nil, err
		}
		out = append(out, real)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func validateSandboxBindOverlap(
	writable []string,
	readOnly []string,
) error {
	for _, writablePath := range writable {
		for _, readOnlyPath := range readOnly {
			if pathsOverlap(writablePath, readOnlyPath) {
				return fmt.Errorf(
					"sandbox bind paths overlap: %s and %s",
					writablePath,
					readOnlyPath,
				)
			}
		}
	}
	return nil
}

func buildAllowedEnvSet(keys []string) map[string]struct{} {
	source := keys
	if len(source) == 0 {
		source = defaultSandboxEnvKeys
	}
	out := make(map[string]struct{}, len(source))
	for _, key := range source {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		out[trimmed] = struct{}{}
	}
	out["PATH"] = struct{}{}
	out["HOME"] = struct{}{}
	out["TMPDIR"] = struct{}{}
	return out
}

func collectSandboxBaselineEnv(
	allowed map[string]struct{},
) map[string]string {
	out := make(map[string]string, len(allowed)+len(sandboxFixedEnvKeys))
	for key, value := range sandboxFixedEnvKeys {
		out[key] = value
	}
	for _, key := range []string{"LANG", "LC_ALL", "TERM"} {
		if _, ok := allowed[key]; !ok {
			continue
		}
		if value, found := os.LookupEnv(key); found && value != "" {
			out[key] = value
		}
	}
	if _, ok := out["PATH"]; !ok {
		if value, found := os.LookupEnv("PATH"); found && value != "" {
			out["PATH"] = value
		} else {
			out["PATH"] = "/usr/bin:/bin"
		}
	}
	return out
}

func filterSandboxEnvMap(
	env map[string]string,
	allowed map[string]struct{},
) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, fixed := sandboxFixedEnvKeys[trimmed]; fixed {
			continue
		}
		if _, ok := allowed[trimmed]; !ok {
			continue
		}
		out[trimmed] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeSandboxEnv(
	cfg resolvedSandboxedConfig,
	extra map[string]string,
) map[string]string {
	out := make(map[string]string, len(cfg.baselineEnv)+len(cfg.baseEnv)+len(extra))
	for key, value := range cfg.baselineEnv {
		out[key] = value
	}
	for key, value := range cfg.baseEnv {
		out[key] = value
	}
	for key, value := range filterSandboxEnvMap(extra, cfg.allowedEnvSet) {
		out[key] = value
	}
	return out
}

func existingSandboxSystemPaths() []string {
	out := make([]string, 0, len(defaultSandboxSystemPaths))
	for _, path := range defaultSandboxSystemPaths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			out = append(out, path)
		}
	}
	return out
}

func pathHasSystemBind(path string, systemBinds []string) bool {
	for _, root := range systemBinds {
		if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func pathWithinBase(base string, target string) bool {
	if base == target {
		return true
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func pathsOverlap(left string, right string) bool {
	return pathWithinBase(left, right) || pathWithinBase(right, left)
}

func resolveSandboxWorkdir(
	raw string,
	baseDir string,
	baseDirReal string,
) (string, error) {
	workdir, err := resolveWorkdir(raw, baseDir)
	if err != nil {
		return "", err
	}
	real, err := realpathDir(workdir)
	if err != nil {
		return "", err
	}
	if !pathWithinBase(baseDirReal, real) {
		return "", fmt.Errorf("sandbox workdir escapes base dir: %s", real)
	}
	return real, nil
}

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package iterfs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultDirPerm  = 0o755
	defaultFilePerm = 0o644
)

// IterFS manages per-iteration output layout.
type IterFS struct {
	rootDir string
}

// New returns an IterFS rooted at rootDir.
func New(rootDir string) *IterFS {
	return &IterFS{rootDir: rootDir}
}

// Root returns the root directory.
func (fs *IterFS) Root() string {
	return fs.rootDir
}

// IterDir returns the directory for the given iteration (1-based).
func (fs *IterFS) IterDir(iter int) string {
	return filepath.Join(fs.rootDir, fmt.Sprintf("iter_%04d", iter))
}

// EnsureIterDir creates the iteration directory if needed.
func (fs *IterFS) EnsureIterDir(iter int) (string, error) {
	dir := fs.IterDir(iter)
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return "", err
	}
	return dir, nil
}

// WriteFile writes bytes to the relative file path under the iteration directory.
func (fs *IterFS) WriteFile(iter int, rel string, data []byte) (string, error) {
	dir := fs.IterDir(iter)
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), defaultDirPerm); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, defaultFilePerm); err != nil {
		return "", err
	}
	return path, nil
}

// WriteJSON writes v as pretty JSON.
func (fs *IterFS) WriteJSON(iter int, rel string, v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	return fs.WriteFile(iter, rel, data)
}

// ReadFile reads a relative file path under the iteration directory.
func (fs *IterFS) ReadFile(iter int, rel string) ([]byte, string, error) {
	dir := fs.IterDir(iter)
	path := filepath.Join(dir, rel)
	b, err := os.ReadFile(path)
	return b, path, err
}

// CopyFile copies srcPath to destRel under the iteration directory.
func (fs *IterFS) CopyFile(iter int, srcPath, destRel string) (string, error) {
	b, err := os.ReadFile(srcPath)
	if err != nil {
		return "", err
	}
	return fs.WriteFile(iter, destRel, b)
}

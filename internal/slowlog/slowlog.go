//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package slowlog provides handoff diagnostics for slow internal paths.
package slowlog

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	// EnvSlowMS controls the slow log threshold in milliseconds.
	EnvSlowMS            = "TRPC_AGENT_HANDOFF_DIAG_SLOW_MS"
	defaultSlowThreshold = time.Second
)

var (
	thresholdOnce sync.Once
	threshold     time.Duration
)

// Threshold returns the configured slow log threshold.
func Threshold() time.Duration {
	thresholdOnce.Do(func() {
		threshold = defaultSlowThreshold
		raw := strings.TrimSpace(os.Getenv(EnvSlowMS))
		if raw == "" {
			return
		}
		ms, err := strconv.Atoi(raw)
		if err != nil || ms < 0 {
			return
		}
		threshold = time.Duration(ms) * time.Millisecond
	})
	return threshold
}

// Logf emits a diagnostic log.
func Logf(ctx context.Context, format string, args ...any) {
	log.InfofContext(ctx, "[handoff_diag] "+format, args...)
}

// Slowf emits a diagnostic log when elapsed time crosses the threshold.
func Slowf(ctx context.Context, started time.Time, format string, args ...any) {
	elapsed := time.Since(started)
	if elapsed < Threshold() {
		return
	}
	args = append(args, elapsed)
	log.InfofContext(ctx, "[handoff_diag] "+format+" elapsed=%v", args...)
}

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
	"io"
	"os"
	"time"
)

type sandboxRunner interface {
	Start(
		ctx context.Context,
		req sandboxStartRequest,
	) (sandboxProcess, error)
}

type sandboxStartRequest struct {
	Command string
	Workdir string
	Env     map[string]string
	Timeout time.Duration
}

type sandboxProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() (*os.ProcessState, error)
	SignalTerminate() error
	ForceKill() error
	Close() error
}

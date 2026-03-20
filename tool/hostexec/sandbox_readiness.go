//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import "fmt"

func probeSandboxReadiness(cfg resolvedSandboxedConfig) error {
	if cfg.bwrapPath == "" {
		return fmt.Errorf("sandbox bwrap path is empty")
	}
	if cfg.baseDirReal == "" {
		return fmt.Errorf("sandbox base dir is empty")
	}
	if cfg.shellPath == "" {
		return fmt.Errorf("sandbox shell path is empty")
	}
	return nil
}

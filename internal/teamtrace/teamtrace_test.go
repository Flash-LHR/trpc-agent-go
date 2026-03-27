//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package teamtrace

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
)

func TestRootNodeID_PrefersMountedSurfaceRoot(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("trace/team"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			CustomAgentConfigs: surfacepatch.WithRootNodeID(
				nil,
				"workflow/team",
			),
		}),
	)
	require.Equal(t, "workflow/team", RootNodeID(inv, "team"))
}

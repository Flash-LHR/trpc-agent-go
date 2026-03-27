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

func TestRootNodeID_FallsBackToTraceNodeAndTeamName(t *testing.T) {
	traceInv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("trace/team"),
	)
	require.Equal(t, "trace/team", RootNodeID(traceInv, "team"))
	require.Equal(t, "team", RootNodeID(nil, "team"))
}

func TestTeamTraceNodeIDHelpers(t *testing.T) {
	require.Equal(t, "workflow/team/coordinator", CoordinatorNodeID("workflow/team"))
	require.Equal(t, "workflow/team/member", MemberNodeID("workflow/team", "member"))
}

func TestMemberTraceRoot_ConfigHelpers(t *testing.T) {
	cfgs := map[string]any{"keep": "value"}
	stored := WithMemberTraceRoot(cfgs, "workflow/team")
	require.Equal(t, "workflow/team", MemberTraceRoot(stored))
	require.Equal(t, "value", stored["keep"])
	require.Equal(t, "value", cfgs["keep"])
	require.Empty(t, MemberTraceRoot(nil))
	require.Empty(t, MemberTraceRoot(map[string]any{memberTraceRootConfigsKey: 123}))
	require.Equal(t, cfgs, WithMemberTraceRoot(cfgs, ""))
}

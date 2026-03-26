//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
)

func TestWithSurfacePatchForNode_MergesAndCopiesByValue(t *testing.T) {
	var first SurfacePatch
	first.SetInstruction("first instruction")

	var second SurfacePatch
	second.SetGlobalInstruction("global instruction")

	var third SurfacePatch
	third.SetInstruction("second instruction")

	opts := NewRunOptions(
		WithSurfacePatchForNode("root", first),
		WithSurfacePatchForNode("root", second),
		WithSurfacePatchForNode("root", third),
	)

	patch, ok := surfacepatch.PatchForNode(opts.CustomAgentConfigs, "root")
	require.True(t, ok)

	instruction, ok := patch.Instruction()
	require.True(t, ok)
	require.Equal(t, "second instruction", instruction)

	globalInstruction, ok := patch.GlobalInstruction()
	require.True(t, ok)
	require.Equal(t, "global instruction", globalInstruction)

	first.SetInstruction("mutated")
	patch, ok = surfacepatch.PatchForNode(opts.CustomAgentConfigs, "root")
	require.True(t, ok)

	instruction, ok = patch.Instruction()
	require.True(t, ok)
	require.Equal(t, "second instruction", instruction)
}

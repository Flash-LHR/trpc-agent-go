//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package structure

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func TestRebaseSnapshot_RewritesNodesEdgesAndSurfaces(t *testing.T) {
	text := "instruction"
	snapshot, err := RebaseSnapshot(&structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/child", Kind: structure.NodeKindFunction, Name: "child"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/child"},
		},
		Surfaces: []structure.Surface{
			{
				NodeID: "root/child",
				Type:   structure.SurfaceTypeInstruction,
				Value:  structure.SurfaceValue{Text: &text},
			},
		},
	}, "mounted")
	require.NoError(t, err)
	assert.Equal(t, "mounted", snapshot.EntryNodeID)
	assert.Contains(t, snapshot.Nodes, structure.Node{
		NodeID: "mounted",
		Kind:   structure.NodeKindAgent,
		Name:   "root",
	})
	assert.Contains(t, snapshot.Nodes, structure.Node{
		NodeID: "mounted/child",
		Kind:   structure.NodeKindFunction,
		Name:   "child",
	})
	assert.Contains(t, snapshot.Edges, structure.Edge{
		FromNodeID: "mounted",
		ToNodeID:   "mounted/child",
	})
	assert.Contains(t, snapshot.Surfaces, structure.Surface{
		NodeID:    "mounted/child",
		Type:      structure.SurfaceTypeInstruction,
		Value:     structure.SurfaceValue{Text: &text},
		SurfaceID: "",
	})
}

func TestTerminalNodeIDs_ReturnsEmptyForPureCycle(t *testing.T) {
	terminals := TerminalNodeIDs(&structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/a", Kind: structure.NodeKindFunction, Name: "a"},
			{NodeID: "root/b", Kind: structure.NodeKindFunction, Name: "b"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/a"},
			{FromNodeID: "root/a", ToNodeID: "root/b"},
			{FromNodeID: "root/b", ToNodeID: "root/a"},
			{FromNodeID: "root/b", ToNodeID: "root"},
		},
	})
	assert.Empty(t, terminals)
}

func TestTerminalNodeIDs_IgnoresUnreachableNodes(t *testing.T) {
	terminals := TerminalNodeIDs(&structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/loop", Kind: structure.NodeKindFunction, Name: "loop"},
			{NodeID: "root/loop_back", Kind: structure.NodeKindFunction, Name: "loop_back"},
			{NodeID: "root/tail", Kind: structure.NodeKindFunction, Name: "tail"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/loop"},
			{FromNodeID: "root/loop", ToNodeID: "root/loop_back"},
			{FromNodeID: "root/loop_back", ToNodeID: "root/loop"},
		},
	})
	assert.Empty(t, terminals)
}

func TestRootOnly_KeepsEntryNodeAndEntrySurfaces(t *testing.T) {
	text := "root"
	snapshot := RootOnly(&structure.Snapshot{
		EntryNodeID: "root",
		Nodes: []structure.Node{
			{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
			{NodeID: "root/child", Kind: structure.NodeKindFunction, Name: "child"},
		},
		Edges: []structure.Edge{
			{FromNodeID: "root", ToNodeID: "root/child"},
		},
		Surfaces: []structure.Surface{
			{
				NodeID: "root",
				Type:   structure.SurfaceTypeInstruction,
				Value:  structure.SurfaceValue{Text: &text},
			},
			{
				NodeID: "root/child",
				Type:   structure.SurfaceTypeTool,
				Value:  structure.SurfaceValue{Tools: []structure.ToolRef{{ID: "echo"}}},
			},
		},
	})
	assert.Equal(t, "root", snapshot.EntryNodeID)
	assert.Equal(t, []structure.Node{
		{NodeID: "root", Kind: structure.NodeKindAgent, Name: "root"},
	}, snapshot.Nodes)
	assert.Empty(t, snapshot.Edges)
	assert.Equal(t, []structure.Surface{
		{
			NodeID: "root",
			Type:   structure.SurfaceTypeInstruction,
			Value:  structure.SurfaceValue{Text: &text},
		},
	}, snapshot.Surfaces)
}

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package teamtrace provides internal helpers for mounted team node ids.
package teamtrace

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
)

const memberTraceRootConfigsKey = "__trpc_agent_internal_team_member_trace_root__"

// RootNodeID returns the mounted root node id for one team invocation.
func RootNodeID(inv *agent.Invocation, teamName string) string {
	if inv != nil {
		if nodeID := surfacepatch.RootNodeID(
			inv.RunOptions.CustomAgentConfigs,
			agent.InvocationTraceNodeID(inv),
		); nodeID != "" {
			return nodeID
		}
	}
	return istructure.EscapeLocalName(teamName)
}

// CoordinatorNodeID returns the coordinator node id under one team root.
func CoordinatorNodeID(rootNodeID string) string {
	return istructure.JoinNodeID(rootNodeID, "coordinator")
}

// MemberNodeID returns the member node id under one team root.
func MemberNodeID(rootNodeID string, memberName string) string {
	return istructure.JoinNodeID(rootNodeID, memberName)
}

// WithMemberTraceRoot stores the mounted team root in custom configs.
func WithMemberTraceRoot(cfgs map[string]any, rootNodeID string) map[string]any {
	if rootNodeID == "" {
		return cfgs
	}
	out := copyConfigs(cfgs)
	out[memberTraceRootConfigsKey] = rootNodeID
	return out
}

// MemberTraceRoot returns the mounted team root from custom configs.
func MemberTraceRoot(cfgs map[string]any) string {
	if cfgs == nil {
		return ""
	}
	value, ok := cfgs[memberTraceRootConfigsKey]
	if !ok {
		return ""
	}
	rootNodeID, _ := value.(string)
	return rootNodeID
}

func copyConfigs(in map[string]any) map[string]any {
	if in == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

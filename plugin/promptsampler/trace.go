//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptsampler

import "time"

// TraceStatus represents the completion status of a single execution trace.
type TraceStatus string

// Trace status constants.
const (
	// TraceStatusCompleted indicates the trace completed successfully.
	TraceStatusCompleted TraceStatus = "completed"
	// TraceStatusIncomplete indicates the trace did not complete normally.
	TraceStatusIncomplete TraceStatus = "incomplete"
	// TraceStatusFailed indicates the trace failed with an error.
	TraceStatusFailed TraceStatus = "failed"
)

// StepType indicates the type of a trace step.
type StepType string

// Step type constants.
const (
	// StepTypeModel indicates a model/LLM call step.
	StepTypeModel StepType = "model"
	// StepTypeTool indicates a tool execution step.
	StepTypeTool StepType = "tool"
	// StepTypeAgent indicates an agent invocation step.
	StepTypeAgent StepType = "agent"
)

// NodeKind indicates the kind of node in the static structure.
// It aligns with the static graph's node "kind" field so that dynamic traces
// can be correlated with the structure snapshot.
type NodeKind string

// Node kind constants.
const (
	// NodeKindCoordinator indicates a coordinator (root) agent node.
	NodeKindCoordinator NodeKind = "coordinator"
	// NodeKindMember indicates a member (sub) agent node.
	NodeKindMember NodeKind = "member"
	// NodeKindTool indicates a tool node.
	NodeKindTool NodeKind = "tool"
)

// Trace describes a step-DAG of a single Runner execution.
//
// One Trace is produced per root invocation; sub-agent steps are merged into
// the root's Trace to keep correlation simple.
type Trace struct {
	// StructureID identifies the static structure/schema used for this execution.
	StructureID string `json:"structure_id"`
	// InvocationID is the unique identifier of the root invocation.
	InvocationID string `json:"invocation_id"`
	// AgentName is the name of the root agent.
	AgentName string `json:"agent_name"`
	// Status indicates the completion status of this trace.
	Status TraceStatus `json:"status"`
	// Input contains the root invocation input.
	Input *TraceInput `json:"input,omitempty"`
	// FinalOutput contains the final textual output of the execution.
	FinalOutput *TraceOutput `json:"final_output,omitempty"`
	// Steps contains all steps recorded during execution, in completion order.
	Steps []TraceStep `json:"steps"`
	// StartTime is when the root invocation started.
	StartTime time.Time `json:"start_time"`
	// EndTime is when the root invocation ended.
	EndTime time.Time `json:"end_time"`
	// Duration is the total execution duration.
	Duration time.Duration `json:"duration"`
	// Error contains the error message if Status is TraceStatusFailed.
	Error string `json:"error,omitempty"`
}

// TraceStep describes a single node visit in the execution trace.
type TraceStep struct {
	// StepID is a unique identifier within the Trace.
	StepID string `json:"step_id"`
	// NodeID identifies the corresponding static node (agent name or tool name).
	NodeID string `json:"node_id"`
	// StepType indicates the kind of step (model, tool, agent).
	StepType StepType `json:"step_type"`
	// NodeKind indicates the kind of node in the static structure.
	NodeKind NodeKind `json:"node_kind,omitempty"`
	// PredecessorStepIDs contains the IDs of direct predecessors that were hit.
	PredecessorStepIDs []string `json:"predecessor_step_ids,omitempty"`
	// AppliedSurfaceIDs contains the IDs of surfaces that took effect here.
	AppliedSurfaceIDs []string `json:"applied_surface_ids,omitempty"`
	// Input describes the input to this step.
	Input *TraceInput `json:"input,omitempty"`
	// Output describes the output produced by this step.
	Output *TraceOutput `json:"output,omitempty"`
	// Error contains the error message if this step failed.
	Error string `json:"error,omitempty"`
	// StartTime is when this step started.
	StartTime time.Time `json:"start_time"`
	// EndTime is when this step ended.
	EndTime time.Time `json:"end_time"`
	// Duration is how long this step took.
	Duration time.Duration `json:"duration"`
}

// TraceInput describes the input of a step.
type TraceInput struct {
	// Text is a textual representation of the input.
	Text string `json:"text"`
	// ToolName is set when this is a tool input.
	ToolName string `json:"tool_name,omitempty"`
	// ToolArguments contains the raw tool arguments (JSON) if applicable.
	ToolArguments string `json:"tool_arguments,omitempty"`
	// MessageCount is the number of messages in a model input.
	MessageCount int `json:"message_count,omitempty"`
}

// TraceOutput describes the output of a step.
type TraceOutput struct {
	// Text is a textual representation of the output.
	Text string `json:"text"`
	// ToolResult contains the tool result (serialized) if applicable.
	ToolResult string `json:"tool_result,omitempty"`
	// TokenUsage contains LLM token usage for model calls.
	//
	// Note: this field is the *LLM token accounting* (prompt / completion
	// tokens). It is unrelated to the business isolation Token in
	// RuntimeConfig, which identifies the calling tenant.
	TokenUsage *TokenUsage `json:"token_usage,omitempty"`
}

// TokenUsage contains LLM token usage statistics for a model step.
type TokenUsage struct {
	// PromptTokens is the number of tokens in the prompt.
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens is the number of tokens in the completion.
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens is the total number of tokens used.
	TotalTokens int `json:"total_tokens"`
}

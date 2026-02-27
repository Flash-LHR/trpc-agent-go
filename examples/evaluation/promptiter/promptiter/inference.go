//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// InferEvalCases runs candidate inference for a set of eval cases, injecting instruction as the per-run prompt.
func InferEvalCases(
	ctx context.Context,
	r runner.Runner,
	appName string,
	evalSetID string,
	evalCases []*evalset.EvalCase,
	instruction string,
	sessionIDSupplier func() string,
) []*service.InferenceResult {
	if sessionIDSupplier == nil {
		sessionIDSupplier = func() string { return "" }
	}
	results := make([]*service.InferenceResult, 0, len(evalCases))
	for _, ec := range evalCases {
		results = append(results, inferOneEvalCase(ctx, r, appName, evalSetID, ec, instruction, sessionIDSupplier))
	}
	return results
}

func inferOneEvalCase(
	ctx context.Context,
	r runner.Runner,
	appName string,
	evalSetID string,
	ec *evalset.EvalCase,
	instruction string,
	sessionIDSupplier func() string,
) *service.InferenceResult {
	sessionID := sessionIDSupplier()
	res := &service.InferenceResult{
		AppName:    appName,
		EvalSetID:  evalSetID,
		SessionID:  sessionID,
		EvalCaseID: "",
		EvalMode:   evalset.EvalModeDefault,
		UserID:     "",
	}
	if ec == nil {
		return failedInferenceResult(res, errors.New("eval case is nil"))
	}
	res.EvalCaseID = ec.EvalID
	res.EvalMode = ec.EvalMode
	if ec.SessionInput != nil {
		res.UserID = ec.SessionInput.UserID
	}
	if ec.SessionInput == nil {
		return failedInferenceResult(res, errors.New("session input is nil"))
	}
	if len(ec.Conversation) == 0 {
		return failedInferenceResult(res, errors.New("invocations are empty"))
	}
	for _, inv := range ec.Conversation {
		if inv == nil || inv.UserContent == nil {
			return failedInferenceResult(res, errors.New("invocation user content is nil"))
		}
		if err := validateUserJSON(inv.UserContent.Content); err != nil {
			return failedInferenceResult(res, err)
		}
	}
	// Prepare injected context messages.
	contextMessages, err := buildSeedMessages(ec.ContextMessages)
	if err != nil {
		return failedInferenceResult(res, err)
	}
	// Run each invocation and capture responses.
	responseInvocations := make([]*evalset.Invocation, 0, len(ec.Conversation))
	for _, inv := range ec.Conversation {
		responseInvocation, err := inferenceInvocation(ctx, r, sessionID, ec.SessionInput, inv, contextMessages, instruction)
		if err != nil {
			return failedInferenceResult(res, err)
		}
		responseInvocations = append(responseInvocations, responseInvocation)
	}
	res.Inferences = responseInvocations
	res.Status = status.EvalStatusPassed
	return res
}

func failedInferenceResult(res *service.InferenceResult, err error) *service.InferenceResult {
	res.Status = status.EvalStatusFailed
	res.ErrorMessage = err.Error()
	res.Inferences = nil
	return res
}

func validateUserJSON(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return errors.New("user content is empty")
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return fmt.Errorf("user content is not valid JSON: %w", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return errors.New("user content JSON must be an object")
	}
	return nil
}

func inferenceInvocation(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	initialSession *evalset.SessionInput,
	invocation *evalset.Invocation,
	contextMessages []model.Message,
	instruction string,
) (*evalset.Invocation, error) {
	if invocation.UserContent == nil {
		return nil, fmt.Errorf("invocation user content is nil for eval case invocation %q", invocation.InvocationID)
	}
	events, err := r.Run(
		ctx,
		initialSession.UserID,
		sessionID,
		*invocation.UserContent,
		agent.WithRuntimeState(initialSession.State),
		agent.WithInjectedContextMessages(contextMessages),
		agent.WithInstruction(instruction),
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	var (
		invocationID  string
		finalResponse *model.Message
		tools         = make([]*evalset.Tool, 0)
		toolIDIdx     = make(map[string]int)
	)
	for e := range events {
		if e == nil {
			continue
		}
		if e.Error != nil {
			return nil, fmt.Errorf("event: %v", e.Error)
		}
		if invocationID == "" && e.InvocationID != "" {
			invocationID = e.InvocationID
		}
		if e.IsFinalResponse() {
			if len(e.Response.Choices) == 0 {
				return nil, errors.New("final response has no choices")
			}
			finalResponse = &e.Response.Choices[0].Message
			continue
		}
		if e.IsToolCallResponse() {
			toolcalls, err := convertTools(e)
			if err != nil {
				return nil, fmt.Errorf("convert tool call response: %w", err)
			}
			for _, toolcall := range toolcalls {
				tools = append(tools, toolcall)
				toolIDIdx[toolcall.ID] = len(tools) - 1
			}
		}
		if e.IsToolResultResponse() {
			if err := mergeToolResultResponse(e, toolIDIdx, tools); err != nil {
				return nil, fmt.Errorf("convert tool result response: %w", err)
			}
		}
	}
	if invocationID == "" {
		invocationID = invocation.InvocationID
	}
	contextPtrs := make([]*model.Message, 0, len(contextMessages))
	for i := range contextMessages {
		contextPtrs = append(contextPtrs, &contextMessages[i])
	}
	return &evalset.Invocation{
		InvocationID:    invocationID,
		ContextMessages: contextPtrs,
		UserContent:     invocation.UserContent,
		FinalResponse:   finalResponse,
		Tools:           tools,
	}, nil
}

func convertTools(e *event.Event) ([]*evalset.Tool, error) {
	tools := []*evalset.Tool{}
	for _, choice := range e.Response.Choices {
		for _, toolCall := range choice.Message.ToolCalls {
			tools = append(tools, &evalset.Tool{
				ID:        toolCall.ID,
				Name:      toolCall.Function.Name,
				Arguments: parseToolCallArguments(toolCall.Function.Arguments),
			})
		}
	}
	return tools, nil
}

func parseToolCallArguments(arguments []byte) any {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err == nil {
		return value
	}
	return string(arguments)
}

func mergeToolResultResponse(e *event.Event, toolIDIdx map[string]int, tools []*evalset.Tool) error {
	for _, choice := range e.Response.Choices {
		toolID := choice.Message.ToolID
		idx, ok := toolIDIdx[toolID]
		if !ok {
			return fmt.Errorf("tool ID %s not found in tool ID index for tool result response", toolID)
		}
		tools[idx].Result = parseToolResultContent(choice.Message.Content)
	}
	return nil
}

func parseToolResultContent(content string) any {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err == nil {
		return value
	}
	return content
}

func buildSeedMessages(messages []*model.Message) ([]model.Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	seed := make([]model.Message, 0, len(messages))
	for idx, message := range messages {
		if message == nil {
			return nil, fmt.Errorf("context message is nil at index %d", idx)
		}
		seed = append(seed, *message)
	}
	return seed, nil
}

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolcallid

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const canonicalToolCallIDPrefix = "trpc-agent-go-toolcall"

func canonicalizeResponse(inv *agent.Invocation, rsp *model.Response) (*model.Response, error) {
	if rsp == nil || rsp.IsPartial {
		return nil, nil
	}
	hasToolCalls := false
	for _, choice := range rsp.Choices {
		if len(choice.Message.ToolCalls) > 0 || len(choice.Delta.ToolCalls) > 0 {
			hasToolCalls = true
			break
		}
	}
	if !hasToolCalls {
		return nil, nil
	}
	if inv == nil {
		return nil, fmt.Errorf("canonicalizing tool call ids: invocation is nil")
	}
	if inv.InvocationID == "" {
		return nil, fmt.Errorf("canonicalizing tool call ids: invocation id is empty")
	}
	if rsp.ID == "" {
		return nil, fmt.Errorf("canonicalizing tool call ids: response id is empty")
	}
	cloned := rsp.Clone()
	for i, choice := range rsp.Choices {
		clonedChoice := cloned.Choices[i]
		canonicalizedMessageToolCalls, err := canonicalizeToolCalls(
			choice.Message.ToolCalls,
			inv.InvocationID,
			rsp.ID,
			choice.Index,
		)
		if err != nil {
			return nil, err
		}
		if canonicalizedMessageToolCalls != nil {
			clonedMessage := clonedChoice.Message
			clonedMessage.ToolCalls = canonicalizedMessageToolCalls
			clonedChoice.Message = clonedMessage
		}
		canonicalizedDeltaToolCalls, err := canonicalizeToolCalls(
			choice.Delta.ToolCalls,
			inv.InvocationID,
			rsp.ID,
			choice.Index,
		)
		if err != nil {
			return nil, err
		}
		if canonicalizedDeltaToolCalls != nil {
			clonedDelta := clonedChoice.Delta
			clonedDelta.ToolCalls = canonicalizedDeltaToolCalls
			clonedChoice.Delta = clonedDelta
		}
		cloned.Choices[i] = clonedChoice
	}
	return cloned, nil
}

func canonicalizeToolCalls(
	toolCalls []model.ToolCall,
	invocationID string,
	responseID string,
	choiceIndex int,
) ([]model.ToolCall, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	canonicalized := append([]model.ToolCall(nil), toolCalls...)
	for slotIndex := range canonicalized {
		rawToolCallID := toolCalls[slotIndex].ID
		if rawToolCallID == "" {
			return nil, fmt.Errorf(
				"canonicalizing tool call ids: tool call id is empty at choice %d slot %d",
				choiceIndex,
				slotIndex,
			)
		}
		canonicalized[slotIndex].ID = canonicalToolCallID(
			invocationID,
			responseID,
			rawToolCallID,
			choiceIndex,
			slotIndex,
		)
	}
	return canonicalized, nil
}

func canonicalToolCallID(
	invocationID string,
	responseID string,
	rawToolCallID string,
	choiceIndex int,
	slotIndex int,
) string {
	return fmt.Sprintf(
		"%s:%s:%s:%s:c%d:t%d",
		canonicalToolCallIDPrefix,
		invocationID,
		responseID,
		rawToolCallID,
		choiceIndex,
		slotIndex,
	)
}

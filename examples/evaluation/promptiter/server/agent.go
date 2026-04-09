//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	candidateAgentName = "candidate"
)

func newCandidateAgent(m model.Model) (agent.Agent, error) {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      true,
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction("Write one Chinese sentence that summarizes the JSON input. Output only the text."),
		llmagent.WithDescription("Candidate agent for the PromptIter sports commentary example."),
		llmagent.WithGenerationConfig(generationConfig),
	), nil
}

func newPromptIterWorkerAgent(m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
	}
	return llmagent.New(
		"promptiter-worker",
		llmagent.WithModel(m),
		llmagent.WithInstruction("You are a careful PromptIter worker. Follow the user's request exactly and produce valid JSON when structured output is enabled."),
		llmagent.WithDescription("Worker agent for PromptIter backward, aggregation, and optimization stages."),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func newJudgeAgent(m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		"commentary-judge",
		llmagent.WithModel(m),
		llmagent.WithInstruction("Follow the provided evaluation instructions exactly. Treat the user input as structured JSON with current live game state and recent context. Return only the requested judge output."),
		llmagent.WithDescription("Judge agent for the PromptIter sports commentary rubric evaluation example."),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func newTeacherAgent(m model.Model) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.2),
		Stream:      false,
	}
	return llmagent.New(
		"commentary-teacher",
		llmagent.WithModel(m),
		llmagent.WithInstruction("Read the JSON and write exactly one Chinese sentence of live NBA commentary about the current play. Sound like a broadcaster reacting in the moment: natural, spoken, vivid, and punchy, not like a recap or formal report. Prioritize the current_event or most immediate action over overall game summary. Mention the key player or team, describe the main action, and include at least one explicit factual detail directly grounded from the JSON; when available, prefer exact game-state details such as clock, score, margin, quarter, shot or free-throw count, location, defender, possession, or result. When the JSON clearly supports more than one decisive detail, use the richest immediate combination without turning the sentence into a list. Keep the sentence concise, fact-first, and ready for live audio. Do not add unsupported evaluation, inferred consequences, or generic recap. Output only the Chinese sentence."),
		llmagent.WithDescription("Teacher agent that generates reference live commentary for evaluation."),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

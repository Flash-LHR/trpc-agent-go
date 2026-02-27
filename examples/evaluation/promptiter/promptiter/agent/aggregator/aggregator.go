//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/promptiter/issues"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Aggregator aggregates raw issues into a single prompt gradient.
type Aggregator struct {
	runner     runner.Runner
	promptTmpl *template.Template
}

// New creates a new gradient aggregator.
func New(m model.Model, gen model.GenerationConfig, promptTemplatePath string, outputSchema map[string]any) (*Aggregator, error) {
	if m == nil {
		return nil, errors.New("model is nil")
	}
	if promptTemplatePath == "" {
		return nil, errors.New("prompt template path is empty")
	}
	if outputSchema == nil {
		return nil, errors.New("output schema is nil")
	}
	// Load prompt template.
	b, err := os.ReadFile(promptTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("read aggregator prompt: %w", err)
	}
	tmpl, err := template.New("gradient_aggregator").Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("parse aggregator prompt template: %w", err)
	}
	// Build runner.
	gen.Stream = false
	ag := llmagent.New(
		"gradient_aggregator",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("aggregated_gradient", outputSchema, true, "Aggregated prompt gradient."),
	)
	return &Aggregator{
		runner:     runner.NewRunner("promptiter_aggregator", ag),
		promptTmpl: tmpl,
	}, nil
}

// Close releases resources owned by the aggregator.
func (a *Aggregator) Close() error {
	if a == nil || a.runner == nil {
		return nil
	}
	return a.runner.Close()
}

// Aggregate runs the LLM aggregator and parses the aggregated gradient.
func (a *Aggregator) Aggregate(
	ctx context.Context,
	promptSectionIDs []string,
	rawIssues []issues.IssueRecord,
	examples any,
) (*issues.AggregatedGradient, string, error) {
	if a.runner == nil || a.promptTmpl == nil {
		return nil, "", errors.New("aggregator is not initialized")
	}
	// Prepare JSON payloads for the prompt template.
	rawIssuesJSON, err := json.Marshal(rawIssues)
	if err != nil {
		return nil, "", fmt.Errorf("marshal raw issues: %w", err)
	}
	sectionsJSON, err := json.Marshal(promptSectionIDs)
	if err != nil {
		return nil, "", fmt.Errorf("marshal prompt sections: %w", err)
	}
	examplesJSON, err := json.Marshal(examples)
	if err != nil {
		return nil, "", fmt.Errorf("marshal examples: %w", err)
	}
	// Render prompt.
	prompt, err := a.render(promptTemplateData{
		PromptSections: string(sectionsJSON),
		RawIssues:      string(rawIssuesJSON),
		Examples:       string(examplesJSON),
	})
	if err != nil {
		return nil, "", err
	}
	// Call once and parse, then retry once on JSON parse failure.
	raw, err := a.callOnce(ctx, prompt)
	if err != nil {
		return nil, "", err
	}
	parsed, perr := parseAggregatedGradient(raw)
	if perr == nil {
		return parsed, strings.TrimSpace(raw), nil
	}
	raw2, err2 := a.callOnce(ctx, prompt)
	if err2 != nil {
		return nil, strings.TrimSpace(raw), perr
	}
	parsed2, perr2 := parseAggregatedGradient(raw2)
	if perr2 != nil {
		return nil, strings.TrimSpace(raw2), perr2
	}
	return parsed2, strings.TrimSpace(raw2), nil
}

func (a *Aggregator) render(data promptTemplateData) (string, error) {
	var buf bytes.Buffer
	if err := a.promptTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render aggregator prompt: %w", err)
	}
	return buf.String(), nil
}

func (a *Aggregator) callOnce(ctx context.Context, prompt string) (string, error) {
	sessionID := uuid.NewString()
	events, err := a.runner.Run(ctx, "aggregator_user", sessionID, model.Message{Role: model.RoleUser, Content: prompt})
	if err != nil {
		return "", fmt.Errorf("run aggregator: %w", err)
	}
	return captureFinalContent(events)
}

func captureFinalContent(events <-chan *event.Event) (string, error) {
	var final *model.Message
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return "", fmt.Errorf("aggregator event error: %v", evt.Error)
		}
		if evt.IsFinalResponse() {
			if len(evt.Response.Choices) == 0 {
				return "", errors.New("aggregator final response has no choices")
			}
			final = &evt.Response.Choices[0].Message
		}
	}
	if final == nil {
		return "", errors.New("aggregator did not return a final response")
	}
	return final.Content, nil
}

func parseAggregatedGradient(raw string) (*issues.AggregatedGradient, error) {
	var out issues.AggregatedGradient
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type promptTemplateData struct {
	// PromptSections is the JSON-encoded list of prompt section ids.
	PromptSections string
	// RawIssues is the JSON-encoded list of per-case issues.
	RawIssues string
	// Examples is the JSON-encoded list of representative examples.
	Examples string
}

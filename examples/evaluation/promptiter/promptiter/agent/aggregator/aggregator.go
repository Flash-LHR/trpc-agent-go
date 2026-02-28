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
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Config defines an aggregator agent configuration.
type Config struct {
	// ProviderName is the provider registry name used by provider.Model.
	ProviderName string
	// ModelName is the model identifier passed to the provider.
	ModelName string
	// BaseURL is the optional OpenAI-compatible endpoint base URL.
	BaseURL string
	// APIKey is the API key used by the provider.
	APIKey string
	// Generation controls sampling and token limits for the model.
	Generation model.GenerationConfig
	// PromptTemplatePath is the Go template used to build the aggregator prompt.
	PromptTemplatePath string
	// OutputSchemaPath is the JSON schema file used for aggregated gradient structured outputs.
	OutputSchemaPath string
}

// Aggregator aggregates raw issues into a single prompt gradient.
type Aggregator struct {
	runner     runner.Runner
	promptTmpl *template.Template
}

// New creates a new gradient aggregator.
func New(cfg Config) (*Aggregator, error) {
	if strings.TrimSpace(cfg.ProviderName) == "" {
		return nil, errors.New("provider name is empty")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return nil, errors.New("model name is empty")
	}
	if cfg.PromptTemplatePath == "" {
		return nil, errors.New("prompt template path is empty")
	}
	if strings.TrimSpace(cfg.OutputSchemaPath) == "" {
		return nil, errors.New("output schema path is empty")
	}
	schemaBytes, err := os.ReadFile(cfg.OutputSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read output schema: %w", err)
	}
	var outputSchema map[string]any
	if err := json.Unmarshal(schemaBytes, &outputSchema); err != nil {
		return nil, fmt.Errorf("unmarshal output schema: %w", err)
	}
	// Load prompt template.
	b, err := os.ReadFile(cfg.PromptTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("read aggregator prompt: %w", err)
	}
	tmpl, err := template.New("gradient_aggregator").Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("parse aggregator prompt template: %w", err)
	}
	// Build model.
	opts := make([]provider.Option, 0, 3)
	if strings.TrimSpace(cfg.APIKey) != "" {
		opts = append(opts, provider.WithAPIKey(cfg.APIKey))
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		opts = append(opts, provider.WithBaseURL(cfg.BaseURL))
	}
	m, err := provider.Model(cfg.ProviderName, cfg.ModelName, opts...)
	if err != nil {
		return nil, fmt.Errorf("create model: %w", err)
	}
	// Build runner.
	gen := cfg.Generation
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
	if a.runner != nil {
		return a.runner.Close()
	}
	return nil
}

// Aggregate runs the LLM aggregator and parses the aggregated gradient.
func (a *Aggregator) Aggregate(
	ctx context.Context,
	rawIssues []issues.IssueRecord,
) (*issues.AggregatedGradient, error) {
	if a.runner == nil || a.promptTmpl == nil {
		return nil, errors.New("aggregator is not initialized")
	}
	// Prepare JSON payloads for the prompt template.
	rawIssuesJSON, err := json.Marshal(rawIssues)
	if err != nil {
		return nil, fmt.Errorf("marshal raw issues: %w", err)
	}
	// Render prompt.
	prompt, err := a.render(promptTemplateData{
		RawIssues: string(rawIssuesJSON),
	})
	if err != nil {
		return nil, err
	}
	// Call once and parse the aggregated gradient.
	raw, err := a.callOnce(ctx, prompt)
	if err != nil {
		return nil, err
	}
	parsed, perr := parseAggregatedGradient(raw)
	if perr != nil {
		return nil, fmt.Errorf("parse aggregated gradient: %w", perr)
	}
	return parsed, nil
}

func (a *Aggregator) render(data promptTemplateData) (string, error) {
	var buf bytes.Buffer
	if err := a.promptTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render aggregator prompt: %w", err)
	}
	return buf.String(), nil
}

func (a *Aggregator) callOnce(ctx context.Context, prompt string) (string, error) {
	var (
		userID      = "aggregator_user"
		sessionID   = uuid.NewString()
		userMessage = model.Message{Role: model.RoleUser, Content: prompt}
	)
	events, err := a.runner.Run(ctx, userID, sessionID, userMessage)
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
	// RawIssues is the JSON-encoded list of per-case issues.
	RawIssues string
}

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package candidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Config defines a candidate agent configuration.
type Config struct {
	// AppName is the runner name used by evaluation.AgentEvaluator.
	AppName string
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
	// OutputSchemaPath is the JSON schema file used for candidate structured outputs.
	OutputSchemaPath string
}

// Candidate is the model-backed runner used for evaluation inference.
type Candidate struct {
	runner runner.Runner
}

// New creates a new candidate runner instance.
func New(cfg Config) (*Candidate, error) {
	if strings.TrimSpace(cfg.AppName) == "" {
		return nil, errors.New("app name is empty")
	}
	if strings.TrimSpace(cfg.ProviderName) == "" {
		return nil, errors.New("provider name is empty")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return nil, errors.New("model name is empty")
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
	gen := cfg.Generation
	gen.Stream = false
	ag := llmagent.New(
		"candidate",
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("sportscaster_output", outputSchema, true, "Sportscaster output."),
	)
	return &Candidate{runner: runner.NewRunner(cfg.AppName, ag)}, nil
}

// Run executes a single candidate invocation.
func (c *Candidate) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if c == nil || c.runner == nil {
		return nil, errors.New("candidate runner is nil")
	}
	return c.runner.Run(ctx, userID, sessionID, message, runOpts...)
}

// Close releases resources owned by the candidate.
func (c *Candidate) Close() error {
	if c.runner != nil {
		return c.runner.Close()
	}
	return nil
}

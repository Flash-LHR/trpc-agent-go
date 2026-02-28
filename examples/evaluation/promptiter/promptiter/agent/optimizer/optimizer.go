//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimizer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
)

// Config defines an optimizer agent configuration.
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
	// InstructionPath is the instruction file that guides prompt edits.
	InstructionPath string
	// BaseDir is the file tool sandbox root for all optimizer operations.
	BaseDir string
}

// Optimizer edits prompt_after.md in-place via the file toolset.
type Optimizer struct {
	runner      runner.Runner
	fileToolSet tool.ToolSet
}

// New creates a new optimizer using the provided model and instruction file.
// File tools are scoped to baseDir to avoid touching source code.
func New(cfg Config) (*Optimizer, error) {
	if strings.TrimSpace(cfg.ProviderName) == "" {
		return nil, errors.New("provider name is empty")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return nil, errors.New("model name is empty")
	}
	if cfg.InstructionPath == "" {
		return nil, errors.New("instruction path is empty")
	}
	if cfg.BaseDir == "" {
		return nil, errors.New("base dir is empty")
	}
	if err := os.MkdirAll(cfg.BaseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create base dir: %w", err)
	}
	b, err := os.ReadFile(cfg.InstructionPath)
	if err != nil {
		return nil, fmt.Errorf("read optimizer instruction: %w", err)
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
	fileToolSet, err := file.NewToolSet(
		file.WithBaseDir(cfg.BaseDir),
		file.WithName("file"),
	)
	if err != nil {
		return nil, fmt.Errorf("create file toolset: %w", err)
	}
	ag := llmagent.New(
		"prompt_optimizer",
		llmagent.WithModel(m),
		llmagent.WithInstruction(string(b)),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithToolSets([]tool.ToolSet{fileToolSet}),
	)
	r := runner.NewRunner("promptiter_optimizer", ag)
	return &Optimizer{runner: r, fileToolSet: fileToolSet}, nil
}

// Close releases resources owned by the optimizer.
func (o *Optimizer) Close() error {
	var errs []error
	if o.runner != nil {
		errs = append(errs, o.runner.Close())
	}
	if o.fileToolSet != nil {
		errs = append(errs, o.fileToolSet.Close())
	}
	return errors.Join(errs...)
}

// Optimize runs the optimizer agent and returns its final response content.
func (o *Optimizer) Optimize(ctx context.Context, content string) (string, error) {
	if o.runner == nil {
		return "", errors.New("optimizer runner is nil")
	}
	var (
		userID      = uuid.NewString()
		sessionID   = uuid.NewString()
		userMessage = model.NewUserMessage(content)
	)
	// Run and consume the event stream.
	events, err := o.runner.Run(ctx, userID, sessionID, userMessage)
	if err != nil {
		return "", fmt.Errorf("run optimizer: %w", err)
	}
	for e := range events {
		if e == nil {
			continue
		}
		if e.Error != nil {
			return "", fmt.Errorf("optimizer event error: %v", e.Error)
		}
		if e.IsFinalResponse() {
			if len(e.Response.Choices) == 0 {
				return "", errors.New("optimizer final response has no choices")
			}
			return e.Response.Choices[0].Message.Content, nil
		}
	}
	return "", errors.New("optimizer did not return a final response")
}

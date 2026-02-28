//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package teacher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/provider"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Config defines a teacher agent configuration.
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
	// InstructionPath is the stable teacher prompt file path for reference outputs.
	InstructionPath string
	// OutputSchemaPath is the JSON schema file used for teacher structured outputs.
	OutputSchemaPath string
}

// Teacher provides reference outputs and caches them to stabilize judge inputs.
type Teacher struct {
	runner          runner.Runner
	instructionHash string
	schemaHash      string
	cache           *cache
}

// New creates a new teacher agent wrapper with an in-memory cache.
func New(cfg Config) (*Teacher, error) {
	if strings.TrimSpace(cfg.ProviderName) == "" {
		return nil, errors.New("provider name is empty")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return nil, errors.New("model name is empty")
	}
	if strings.TrimSpace(cfg.InstructionPath) == "" {
		return nil, errors.New("instruction path is empty")
	}
	if strings.TrimSpace(cfg.OutputSchemaPath) == "" {
		return nil, errors.New("output schema path is empty")
	}
	instructionBytes, err := os.ReadFile(cfg.InstructionPath)
	if err != nil {
		return nil, fmt.Errorf("read instruction: %w", err)
	}
	instruction := string(instructionBytes)
	if instruction == "" {
		return nil, errors.New("teacher instruction is empty")
	}
	schemaBytes, err := os.ReadFile(cfg.OutputSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read output schema: %w", err)
	}
	if len(schemaBytes) == 0 {
		return nil, errors.New("schema bytes are empty")
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
	// Build runner.
	gen := cfg.Generation
	gen.Stream = false
	ag := llmagent.New(
		"teacher",
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(gen),
		llmagent.WithStructuredOutputJSONSchema("sportscaster_output", outputSchema, true, "Sportscaster output."),
	)
	r := runner.NewRunner("promptiter_teacher", ag)
	return &Teacher{
		runner:          r,
		instructionHash: sha256Hex(instructionBytes),
		schemaHash:      sha256Hex(schemaBytes),
		cache:           newCache(),
	}, nil
}

// Close releases resources owned by the teacher.
func (t *Teacher) Close() error {
	if t.runner != nil {
		return t.runner.Close()
	}
	return nil
}

// Get returns the cached teacher output or runs the teacher if cache misses.
func (t *Teacher) Get(ctx context.Context, user model.Message) (string, error) {
	key := t.cacheKey(user.Content)
	if output, ok := t.cache.get(key); ok {
		return output, nil
	}
	sessionID := uuid.NewString()
	events, err := t.runner.Run(ctx, "teacher_user", sessionID, user)
	if err != nil {
		return "", fmt.Errorf("teacher runner run: %w", err)
	}
	output, err := captureFinalContent(events)
	if err != nil {
		return "", err
	}
	if err := t.cache.put(key, output); err != nil {
		return "", err
	}
	return output, nil
}

func (t *Teacher) cacheKey(userContent string) string {
	material := []byte(t.instructionHash + "\n" + t.schemaHash + "\n" + userContent)
	return sha256Hex(material)
}

func captureFinalContent(events <-chan *event.Event) (string, error) {
	var final *model.Message
	for e := range events {
		if e == nil {
			continue
		}
		if e.Error != nil {
			return "", fmt.Errorf("teacher event: %v", e.Error)
		}
		if e.IsFinalResponse() {
			if len(e.Response.Choices) == 0 {
				return "", errors.New("teacher final response has no choices")
			}
			final = &e.Response.Choices[0].Message
		}
	}
	if final == nil {
		return "", errors.New("teacher did not produce a final response")
	}
	return final.Content, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

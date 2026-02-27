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
	"errors"
	"fmt"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Teacher provides reference outputs and caches them to stabilize judge inputs.
type Teacher struct {
	runner          runner.Runner
	instructionHash string
	schemaHash      string
	cache           *Cache
}

// New creates a new teacher wrapper with an in-memory cache.
func New(r runner.Runner, instruction string, schemaBytes []byte) (*Teacher, error) {
	if r == nil {
		return nil, errors.New("runner is nil")
	}
	if instruction == "" {
		return nil, errors.New("teacher instruction is empty")
	}
	if len(schemaBytes) == 0 {
		return nil, errors.New("schema bytes are empty")
	}
	return &Teacher{
		runner:          r,
		instructionHash: sha256Hex([]byte(instruction)),
		schemaHash:      sha256Hex(schemaBytes),
		cache:           NewCache(),
	}, nil
}

// Get returns the cached teacher output or runs the teacher if cache misses.
func (t *Teacher) Get(ctx context.Context, user model.Message) (string, error) {
	key := t.cacheKey(user.Content)
	if entry, ok := t.cache.Get(key); ok {
		return entry.Output, nil
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
	if err := t.cache.Put(cacheEntry{
		Key:             key,
		InstructionHash: t.instructionHash,
		SchemaHash:      t.schemaHash,
		UserHash:        sha256Hex([]byte(user.Content)),
		Output:          output,
	}); err != nil {
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

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"

	"github.com/google/uuid"
)

const defaultRiskThreshold = 80

// Option configures the built-in guardian reviewer.
type Option func(*options)

// UserIDSupplier returns the user ID used for the internal reviewer run.
type UserIDSupplier func(ctx context.Context, req *Request) (string, error)

// SessionIDSupplier returns the session ID used for the internal reviewer run.
type SessionIDSupplier func(ctx context.Context, req *Request) (string, error)

type options struct {
	systemPrompt      string
	riskThreshold     int
	userIDSupplier    UserIDSupplier
	sessionIDSupplier SessionIDSupplier
}

func newOptions(opts ...Option) *options {
	options := &options{
		systemPrompt:  defaultSystemPromptTemplateText,
		riskThreshold: defaultRiskThreshold,
		userIDSupplier: func(ctx context.Context, req *Request) (string, error) {
			return uuid.New().String(), nil
		},
		sessionIDSupplier: func(ctx context.Context, req *Request) (string, error) {
			return uuid.New().String(), nil
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}
	return options
}

// WithSystemPrompt overrides the built-in guardian system prompt template.
func WithSystemPrompt(prompt string) Option {
	return func(opts *options) {
		opts.systemPrompt = prompt
	}
}

// WithRiskThreshold sets the approval risk threshold used by the reviewer.
func WithRiskThreshold(threshold int) Option {
	return func(opts *options) {
		opts.riskThreshold = threshold
	}
}

// WithUserIDSupplier overrides the user ID supplier for reviewer runs.
func WithUserIDSupplier(supplier UserIDSupplier) Option {
	return func(opts *options) {
		opts.userIDSupplier = supplier
	}
}

// WithSessionIDSupplier overrides the session ID supplier for reviewer runs.
func WithSessionIDSupplier(supplier SessionIDSupplier) Option {
	return func(opts *options) {
		opts.sessionIDSupplier = supplier
	}
}

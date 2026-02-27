//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package teacher

import (
	"errors"
	"sync"
)

type cache struct {
	mu    sync.RWMutex
	byKey map[string]string
}

func newCache() *cache {
	return &cache{byKey: make(map[string]string)}
}

func (c *cache) get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	output, ok := c.byKey[key]
	c.mu.RUnlock()
	return output, ok
}

func (c *cache) put(key string, output string) error {
	if c == nil {
		return errors.New("cache is nil")
	}
	if key == "" {
		return errors.New("cache key is empty")
	}
	c.mu.Lock()
	c.byKey[key] = output
	c.mu.Unlock()
	return nil
}

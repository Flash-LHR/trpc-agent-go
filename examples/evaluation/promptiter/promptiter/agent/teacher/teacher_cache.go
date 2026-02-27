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
	"time"
)

type cacheEntry struct {
	// Key is the cache lookup key.
	Key string `json:"key,omitempty"`
	// InstructionHash is the sha256 hash of the teacher instruction.
	InstructionHash string `json:"instruction_sha256,omitempty"`
	// SchemaHash is the sha256 hash of the output schema.
	SchemaHash string `json:"schema_sha256,omitempty"`
	// UserHash is the sha256 hash of the user content.
	UserHash string `json:"user_sha256,omitempty"`
	// CreatedAt is the creation time in RFC3339Nano format.
	CreatedAt string `json:"created_at,omitempty"`
	// Output is the cached teacher output content.
	Output string `json:"output,omitempty"`
}

// Cache stores teacher outputs in memory by key.
type Cache struct {
	mu    sync.RWMutex
	byKey map[string]cacheEntry
}

// NewCache creates an in-memory cache.
func NewCache() *Cache {
	return &Cache{byKey: make(map[string]cacheEntry)}
}

// Get returns the cache entry for key if present.
func (c *Cache) Get(key string) (cacheEntry, bool) {
	if c == nil {
		return cacheEntry{}, false
	}
	c.mu.RLock()
	entry, ok := c.byKey[key]
	c.mu.RUnlock()
	return entry, ok
}

// Put stores the cache entry.
func (c *Cache) Put(entry cacheEntry) error {
	if c == nil {
		return errors.New("cache is nil")
	}
	if entry.Key == "" {
		return errors.New("cache entry key is empty")
	}
	if entry.CreatedAt == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	c.mu.Lock()
	c.byKey[entry.Key] = entry
	c.mu.Unlock()
	return nil
}

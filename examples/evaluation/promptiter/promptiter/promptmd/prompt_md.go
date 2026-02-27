//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var sectionIDPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// Section represents a stable prompt section started by a "## <section_id>" heading.
type Section struct {
	// ID is the stable section identifier parsed from the heading.
	ID string
	// Heading is the original heading line for validation.
	Heading string
	// Body is the raw markdown content under the heading.
	Body string
}

// Document is a parsed prompt document.
type Document struct {
	// Raw is the original markdown text.
	Raw string
	// Sections contains parsed sections in document order.
	Sections []Section
}

// Parse parses a markdown prompt document into stable sections.
func Parse(md string) (*Document, error) {
	lines := strings.Split(md, "\n")
	type idxSection struct {
		id      string
		heading string
		start   int
	}
	var (
		sections     []Section
		current      *idxSection
		seen         = make(map[string]struct{})
		flushSection = func(end int) error {
			if current == nil {
				return nil
			}
			body := strings.Join(lines[current.start:end], "\n")
			id := current.id
			if _, ok := seen[id]; ok {
				return fmt.Errorf("duplicate section_id: %s", id)
			}
			seen[id] = struct{}{}
			sections = append(sections, Section{
				ID:      id,
				Heading: current.heading,
				Body:    body,
			})
			current = nil
			return nil
		}
	)
	// Scan headings and build stable sections.
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		if err := flushSection(i); err != nil {
			return nil, err
		}
		rawID := strings.TrimSpace(strings.TrimPrefix(line, "## "))
		if rawID == "" {
			return nil, fmt.Errorf("empty section_id at line %d", i+1)
		}
		if strings.ContainsAny(rawID, " \t") {
			return nil, fmt.Errorf("section_id must not contain spaces: %q", rawID)
		}
		if !sectionIDPattern.MatchString(rawID) {
			return nil, fmt.Errorf("invalid section_id %q (expected %s)", rawID, sectionIDPattern.String())
		}
		current = &idxSection{id: rawID, heading: "## " + rawID, start: i + 1}
	}
	if err := flushSection(len(lines)); err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return nil, errors.New("no sections found (expected headings like \"## role\")")
	}
	return &Document{Raw: md, Sections: sections}, nil
}

// SectionIDs returns the section ids in order.
func (d *Document) SectionIDs() []string {
	ids := make([]string, 0, len(d.Sections))
	for _, s := range d.Sections {
		ids = append(ids, s.ID)
	}
	return ids
}

// ValidateStable ensures the section ids are identical and in the same order.
func ValidateStable(before, after *Document) error {
	if before == nil || after == nil {
		return errors.New("document is nil")
	}
	if len(before.Sections) != len(after.Sections) {
		return fmt.Errorf("section count changed: %d -> %d", len(before.Sections), len(after.Sections))
	}
	for i := range before.Sections {
		b := before.Sections[i]
		a := after.Sections[i]
		if b.ID != a.ID {
			return fmt.Errorf("section_id changed at index %d: %s -> %s", i, b.ID, a.ID)
		}
		if strings.TrimSpace(a.Heading) != "## "+a.ID {
			return fmt.Errorf("section heading must be exactly \"## %s\"", a.ID)
		}
	}
	return nil
}

// ChangedSectionIDs returns ids whose bodies differ (exact string compare).
func ChangedSectionIDs(before, after *Document) ([]string, error) {
	if err := ValidateStable(before, after); err != nil {
		return nil, err
	}
	changed := make([]string, 0)
	for i := range before.Sections {
		if before.Sections[i].Body != after.Sections[i].Body {
			changed = append(changed, before.Sections[i].ID)
		}
	}
	return changed, nil
}

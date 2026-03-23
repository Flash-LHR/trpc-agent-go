//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package structure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

var errNilAgent = errors.New("agent is nil")

type exportState struct {
	stack []agent.Agent
}

// Export exports a normalized static structure snapshot for the given agent.
func Export(ctx context.Context, a agent.Agent) (*Snapshot, error) {
	if a == nil {
		return nil, errNilAgent
	}
	state := &exportState{}
	return exportWithState(ctx, a, state)
}

func exportWithState(
	ctx context.Context,
	a agent.Agent,
	state *exportState,
) (*Snapshot, error) {
	if state.containsRecursiveAgentInstance(a) {
		return normalizeSnapshot(opaqueLeafSnapshot(a))
	}
	exporter, ok := a.(Exporter)
	if !ok {
		return normalizeSnapshot(opaqueLeafSnapshot(a))
	}
	state.push(a)
	raw, err := exporter.Export(ctx, state.exportChild)
	state.pop()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("structure exporter returned nil snapshot")
	}
	return normalizeSnapshot(raw)
}

func (s *exportState) exportChild(
	ctx context.Context,
	a agent.Agent,
) (*Snapshot, error) {
	return exportWithState(ctx, a, s)
}

func normalizeSnapshot(raw *Snapshot) (*Snapshot, error) {
	snapshot := cloneSnapshot(raw)
	if snapshot.EntryNodeID == "" {
		return nil, errors.New("entry node id is empty")
	}
	nodeByID := make(map[string]Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID == "" {
			return nil, errors.New("node id is empty")
		}
		if _, exists := nodeByID[node.NodeID]; exists {
			continue
		}
		nodeByID[node.NodeID] = node
	}
	if _, exists := nodeByID[snapshot.EntryNodeID]; !exists {
		return nil, fmt.Errorf("entry node %q does not exist", snapshot.EntryNodeID)
	}
	edges := make([]Edge, 0, len(snapshot.Edges))
	seenEdges := make(map[string]struct{}, len(snapshot.Edges))
	for _, edge := range snapshot.Edges {
		if _, exists := nodeByID[edge.FromNodeID]; !exists {
			return nil, fmt.Errorf("edge from node %q does not exist", edge.FromNodeID)
		}
		if _, exists := nodeByID[edge.ToNodeID]; !exists {
			return nil, fmt.Errorf("edge to node %q does not exist", edge.ToNodeID)
		}
		key := edge.FromNodeID + "->" + edge.ToNodeID
		if _, exists := seenEdges[key]; exists {
			continue
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, edge)
	}
	surfaces := make([]Surface, 0, len(snapshot.Surfaces))
	seenSurfaceTypes := make(map[string]struct{}, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if _, exists := nodeByID[surface.NodeID]; !exists {
			return nil, fmt.Errorf("surface node %q does not exist", surface.NodeID)
		}
		key := surfaceKey(surface.NodeID, surface.Type)
		if _, exists := seenSurfaceTypes[key]; exists {
			return nil, fmt.Errorf(
				"duplicate surface type %q on node %q",
				surface.Type,
				surface.NodeID,
			)
		}
		seenSurfaceTypes[key] = struct{}{}
		surface.SurfaceID = key
		if err := validateSurfaceValue(surface.Type, surface.Value); err != nil {
			return nil, fmt.Errorf("invalid surface %q: %w", key, err)
		}
		surface.Value = normalizeSurfaceValue(surface.Value)
		surfaces = append(surfaces, surface)
	}
	nodes := make([]Node, 0, len(nodeByID))
	for _, node := range nodeByID {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromNodeID != edges[j].FromNodeID {
			return edges[i].FromNodeID < edges[j].FromNodeID
		}
		return edges[i].ToNodeID < edges[j].ToNodeID
	})
	sort.Slice(surfaces, func(i, j int) bool {
		return surfaces[i].SurfaceID < surfaces[j].SurfaceID
	})
	snapshot.Nodes = nodes
	snapshot.Edges = edges
	snapshot.Surfaces = surfaces
	snapshot.StructureID = ""
	hashInput, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	sum := sha256.Sum256(hashInput)
	snapshot.StructureID = "struct_" + hex.EncodeToString(sum[:])
	return snapshot, nil
}

func cloneSnapshot(raw *Snapshot) *Snapshot {
	if raw == nil {
		return &Snapshot{}
	}
	snapshot := &Snapshot{
		StructureID: raw.StructureID,
		EntryNodeID: raw.EntryNodeID,
		Nodes:       append([]Node(nil), raw.Nodes...),
		Edges:       append([]Edge(nil), raw.Edges...),
		Surfaces:    make([]Surface, 0, len(raw.Surfaces)),
	}
	for _, surface := range raw.Surfaces {
		snapshot.Surfaces = append(snapshot.Surfaces, Surface{
			SurfaceID: surface.SurfaceID,
			NodeID:    surface.NodeID,
			Type:      surface.Type,
			Value:     cloneSurfaceValue(surface.Value),
		})
	}
	return snapshot
}

func cloneSurfaceValue(value SurfaceValue) SurfaceValue {
	cloned := SurfaceValue{
		FewShot: cloneFewShot(value.FewShot),
		Tools:   append([]ToolRef(nil), value.Tools...),
		Skills:  append([]SkillRef(nil), value.Skills...),
	}
	if value.Text != nil {
		text := *value.Text
		cloned.Text = &text
	}
	if value.Model != nil {
		modelRef := *value.Model
		cloned.Model = &modelRef
	}
	return cloned
}

func cloneFewShot(value []FewShotExample) []FewShotExample {
	if len(value) == 0 {
		return nil
	}
	out := make([]FewShotExample, len(value))
	for i, example := range value {
		out[i].Messages = append([]FewShotMessage(nil), example.Messages...)
	}
	return out
}

func normalizeSurfaceValue(value SurfaceValue) SurfaceValue {
	value = cloneSurfaceValue(value)
	if len(value.Tools) > 0 {
		sort.Slice(value.Tools, func(i, j int) bool {
			return value.Tools[i].ID < value.Tools[j].ID
		})
		value.Tools = uniqueToolRefs(value.Tools)
	}
	if len(value.Skills) > 0 {
		sort.Slice(value.Skills, func(i, j int) bool {
			return value.Skills[i].ID < value.Skills[j].ID
		})
		value.Skills = uniqueSkillRefs(value.Skills)
	}
	return value
}

func uniqueToolRefs(refs []ToolRef) []ToolRef {
	if len(refs) == 0 {
		return nil
	}
	out := refs[:0]
	var last string
	for i, ref := range refs {
		if i == 0 || ref.ID != last {
			out = append(out, ref)
			last = ref.ID
		}
	}
	return out
}

func uniqueSkillRefs(refs []SkillRef) []SkillRef {
	if len(refs) == 0 {
		return nil
	}
	out := refs[:0]
	var last string
	for i, ref := range refs {
		if i == 0 || ref.ID != last {
			out = append(out, ref)
			last = ref.ID
		}
	}
	return out
}

func surfaceKey(nodeID string, surfaceType SurfaceType) string {
	return nodeID + "#" + string(surfaceType)
}

func opaqueLeafSnapshot(a agent.Agent) *Snapshot {
	name := a.Info().Name
	nodeID := escapeNodeIDSegment(name)
	return &Snapshot{
		EntryNodeID: nodeID,
		Nodes: []Node{
			{
				NodeID: nodeID,
				Kind:   NodeKindAgent,
				Name:   name,
			},
		},
	}
}

func escapeNodeIDSegment(name string) string {
	if name == "" {
		return "_"
	}
	replacer := strings.NewReplacer("~", "~0", "/", "~1")
	escaped := replacer.Replace(name)
	if escaped == "" {
		return "_"
	}
	return escaped
}

func validateSurfaceValue(surfaceType SurfaceType, value SurfaceValue) error {
	switch surfaceType {
	case SurfaceTypeInstruction, SurfaceTypeGlobalInstruction:
		if len(value.FewShot) > 0 || value.Model != nil || len(value.Tools) > 0 || len(value.Skills) > 0 {
			return errors.New("text surface must not carry other value branches")
		}
	case SurfaceTypeFewShot:
		if value.Text != nil || value.Model != nil || len(value.Tools) > 0 || len(value.Skills) > 0 {
			return errors.New("few-shot surface must not carry other value branches")
		}
	case SurfaceTypeModel:
		if value.Text != nil || len(value.FewShot) > 0 || len(value.Tools) > 0 || len(value.Skills) > 0 {
			return errors.New("model surface must not carry other value branches")
		}
	case SurfaceTypeTool:
		if value.Text != nil || len(value.FewShot) > 0 || value.Model != nil || len(value.Skills) > 0 {
			return errors.New("tool surface must not carry other value branches")
		}
	case SurfaceTypeSkill:
		if value.Text != nil || len(value.FewShot) > 0 || value.Model != nil || len(value.Tools) > 0 {
			return errors.New("skill surface must not carry other value branches")
		}
	default:
		return fmt.Errorf("unknown surface type %q", surfaceType)
	}
	return nil
}

func (s *exportState) push(a agent.Agent) {
	s.stack = append(s.stack, a)
}

func (s *exportState) pop() {
	if len(s.stack) == 0 {
		return
	}
	s.stack = s.stack[:len(s.stack)-1]
}

func (s *exportState) containsRecursiveAgentInstance(a agent.Agent) bool {
	for _, current := range s.stack {
		if samePointerAgentInstance(current, a) {
			return true
		}
	}
	return false
}

func samePointerAgentInstance(left agent.Agent, right agent.Agent) bool {
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return false
	}
	if leftValue.Type() != rightValue.Type() {
		return false
	}
	if leftValue.Kind() != reflect.Pointer || rightValue.Kind() != reflect.Pointer {
		return false
	}
	if leftValue.IsNil() || rightValue.IsNil() {
		return false
	}
	return leftValue.Pointer() == rightValue.Pointer()
}

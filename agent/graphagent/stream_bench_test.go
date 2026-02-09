package graphagent_test

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type benchStreamModel struct {
	name       string
	token      string
	tokenCount int
}

func (m benchStreamModel) Info() model.Info { return model.Info{Name: m.name} }

func (m benchStreamModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if req == nil {
		return nil, errors.New("request cannot be nil")
	}
	if m.tokenCount <= 0 {
		return nil, errors.New("tokenCount must be > 0")
	}
	ch := make(chan *model.Response, m.tokenCount+1)
	go func() {
		defer close(ch)
		id := "bench"
		partial := &model.Response{
			ID:        id,
			Object:    model.ObjectTypeChatCompletionChunk,
			Model:     m.name,
			Done:      false,
			IsPartial: true,
			Choices: []model.Choice{
				{
					Index: 0,
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: m.token,
					},
				},
			},
		}
		for i := 0; i < m.tokenCount; i++ {
			select {
			case ch <- partial:
			case <-ctx.Done():
				return
			}
		}
		final := &model.Response{
			ID:        id,
			Object:    model.ObjectTypeChatCompletion,
			Model:     m.name,
			Done:      true,
			IsPartial: false,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "",
					},
				},
			},
		}
		select {
		case ch <- final:
		case <-ctx.Done():
			return
		}
	}()
	return ch, nil
}

func BenchmarkGraphAgent_StreamMessages(b *testing.B) {
	const tokenCount = 8
	mock := benchStreamModel{name: "mock", token: "hello", tokenCount: tokenCount}
	g, err := graph.NewStateGraph(graph.MessagesStateSchema()).
		AddLLMNode(
			"llm",
			mock,
			"",
			nil,
			graph.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		).
		SetEntryPoint("llm").
		SetFinishPoint("llm").
		Compile()
	if err != nil {
		b.Fatal(err)
	}
	agt, err := graphagent.New("bench", g)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inv := agent.NewInvocation(
			agent.WithInvocationID("bench"),
			agent.WithInvocationMessage(model.NewUserMessage("hello")),
			agent.WithInvocationAgent(agt),
			agent.WithInvocationRunOptions(agent.RunOptions{
				StreamModeEnabled: true,
				StreamModes:       []agent.StreamMode{agent.StreamModeMessages},
			}),
		)
		ctx := agent.NewInvocationContext(context.Background(), inv)
		ch, err := agt.Run(ctx, inv)
		if err != nil {
			b.Fatal(err)
		}
		for range ch {
		}
	}
}

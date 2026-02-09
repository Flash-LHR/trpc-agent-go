package graph_test

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type benchBuildMockModel struct{}

func (benchBuildMockModel) Info() model.Info { return model.Info{Name: "mock"} }

func (benchBuildMockModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	return nil, errors.New("not implemented")
}

func BenchmarkBuildSingleLLMGraph(b *testing.B) {
	b.ReportAllocs()
	mock := benchBuildMockModel{}
	for i := 0; i < b.N; i++ {
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
		_ = agt
	}
}

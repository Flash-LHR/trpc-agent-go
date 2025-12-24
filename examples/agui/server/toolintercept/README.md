# Tool Intercept Demo

本示例演示如何在使用 `AddToolsConditionalEdges` 的同时，在普通节点中拦截部分工具调用，并把剩余调用交给内置 Tools 节点。

## 运行

```bash
go run ./examples/agui/server/toolintercept
```

默认使用 OpenAI `gpt-4o-mini`，需要在环境中配置 `OPENAI_API_KEY`。可通过参数调整模型与监听地址：

```bash
go run ./examples/agui/server/toolintercept -model gpt-4o-mini -address 0.0.0.0:8080 -path /agui
```

浏览器访问 `http://127.0.0.1:8080/agui`，输入任意消息：

- `llm` 使用真实 LLM（OpenAI 接口）生成 `calculator` 工具调用。
- `tool_handler` 在普通节点中处理所有工具调用（无静态 Tools 节点、无 GoTo 跳转），直接生成 `tool` 消息写回状态。
- `final` 汇总工具结果并返回最终回复。

如果末尾助理消息没有 `ToolCalls`，`AddToolsConditionalEdges` 会直接走 `fallback`，跳过工具处理节点。

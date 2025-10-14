## 目标

使用框架已有的可观测上报能力，在 agui runner 中上报 AGUI 事件

## span 示例

### 已有的 span 结构

multi-tool-assistant 是 agent name

```
multi-tool-assistant
├── multi-tool-assistant
│   ├── invoke-agent multi-tool-assistant
│   │   ├── call_llm
│   │   │   └── execute_tool time_tool
│   │   ├── call_llm
│   │   │   └── execute_tool calculator
│   │   └── call_llm
```

### 预期的 span 结构

```
multi-tool-assistant
├── multi-tool-assistant
│   ├── invoke-agent multi-tool-assistant
│   │   ├── call_llm
│   │   │   └── execute_tool time_tool
│   │   ├── call_llm
│   │   │   └── execute_tool calculator
│   │   └── call_llm
│   ├── agui
│   │   ├── agui_run
│   │   │   ├── agui_text
│   │   │   ├── agui_tool
│   │   │   │   ├── agui_tool_call
│   │   │   │   └── agui_tool_response
│   │   │   ├── agui_text
│   │   │   ├── agui_tool
│   │   │   │   ├── agui_tool_call
│   │   │   │   └── agui_tool_response
│   │   │   └── agui_text
```

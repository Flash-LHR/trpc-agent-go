# trpc-agent-go Static Structure Export 设计文档

## 1. 目标

Static Structure Export 是 `trpc-agent-go` 的框架级静态结构导出能力，用于稳定导出当前 Agent 系统的静态节点、静态可能边和可编辑 surface 基线值。

这项能力服务以下场景：

1. PromptIter 这类算法能力的结构基线输入。
2. prompt 管理、配置可视化、结构诊断等静态消费场景。
3. 与 Execution Trace 共享 `NodeID`，形成“静态结构图 + 执行图”的统一身份体系。

## 2. 范围与边界

本设计覆盖以下运行时：

1. `LLMAgent`
2. `GraphAgent`
3. `ChainAgent`
4. `ParallelAgent`
5. `CycleAgent`
6. `Team/Swarm`
7. graph 内部的 `function`、`llm`、`tool`、`agent` node

本设计不负责以下内容：

1. 不导出运行期 step、step 前驱、输入输出快照。
2. 不导出任意业务 state、session 内容或历史消息。
3. 不导出某次运行实际生效的 surface 值。
4. 不把静态结构图和执行图合并成一个公共对象。

## 3. 与 Execution Trace 的关系

Static Structure Export 与 Execution Trace 是两项并列的框架能力。

两者共享以下约束：

1. trace 中出现的 `NodeID` 必须可由结构导出稳定导出。
2. surface 的 `NodeID` 必须落到结构图中的某个静态节点上。
3. 组合 agent 的根节点必须具有稳定 `NodeID`，供结构消费方和执行图共同复用。

两者应当一起设计，但分开实现。

## 4. 公共 API

新增公共子包：

```text
/agent/structure
```

公共入口与可选扩展接口如下：

```go
package structure

type ChildExporter func(ctx context.Context, a agent.Agent) (*Snapshot, error)

type Exporter interface {
    Export(ctx context.Context, exportChild ChildExporter) (*Snapshot, error)
}

func Export(ctx context.Context, a agent.Agent) (*Snapshot, error)
```

API 规则如下：

1. 消费方统一通过 `structure.Export(ctx, agent)` 获取结构快照。
2. 自定义类型若要参与深度结构导出，需要同时满足 `agent.Agent` 和 `structure.Exporter`。
3. `structure.Exporter` 只表达结构导出能力，不内嵌 `agent.Agent`。
4. `structure.Export` 负责统一判空、能力探测、递归会话、内置运行时分发、fallback、规范化和错误语义。
5. built-in 导出器和自定义导出器在递归遇到 child agent 时，统一使用 `exportChild(ctx, child)` 获取子结构，再按当前挂载路径重写并合并。
6. `agent.Agent` 主接口不增加 `Export` 方法。

## 5. 公共模型

```go
package structure

type NodeKind string

const (
    NodeKindAgent    NodeKind = "agent"
    NodeKindLLM      NodeKind = "llm"
    NodeKindFunction NodeKind = "function"
    NodeKindTool     NodeKind = "tool"
)

type SurfaceType string

const (
    SurfaceTypeInstruction       SurfaceType = "instruction"
    SurfaceTypeGlobalInstruction SurfaceType = "global_instruction"
    SurfaceTypeFewShot           SurfaceType = "few_shot"
    SurfaceTypeModel             SurfaceType = "model"
    SurfaceTypeTool              SurfaceType = "tool"
    SurfaceTypeSkill             SurfaceType = "skill"
)

type Snapshot struct {
    StructureID string
    EntryNodeID string
    Nodes       []Node
    Edges       []Edge
    Surfaces    []Surface
}

type Node struct {
    NodeID string
    Kind   NodeKind
    Name   string
}

type Edge struct {
    FromNodeID string
    ToNodeID   string
}

type Surface struct {
    SurfaceID string
    NodeID    string
    Type      SurfaceType
    Value     SurfaceValue
}

type SurfaceValue struct {
    Text    *string
    FewShot []FewShotExample
    Model   *ModelRef
    Tools   []ToolRef
    Skills  []SkillRef
}

type FewShotExample struct {
    Messages []FewShotMessage
}

type FewShotMessage struct {
    Role    string
    Content string
}

type ModelRef struct {
    Provider string
    Name     string
}

type ToolRef struct {
    ID string
}

type SkillRef struct {
    ID          string
    Description string
}
```

模型约束如下：

1. `Snapshot.EntryNodeID` 表示整个静态结构图的根入口节点。
2. `Node.Kind` 只表达节点语义，不表达该节点来自哪类运行时。
3. `Surface.Value` 表示结构基线值，不表示运行时实际生效值。
4. `SurfaceValue` 是受 `SurfaceType` 判别的 union；任意时刻只允许与目标 `SurfaceType` 对应的那一支有值。
5. `few_shot` 表示节点级显式基线样例集合，不等于 session 历史、one-shot messages 或运行时动态拼接内容。

## 6. 身份规则

### 6.1 NodeID

`NodeID` 必须在同一个 `Snapshot` 内唯一，并在相同结构下稳定不变。消费方必须把它当作 opaque key 使用。

内置运行时使用挂载路径生成 `NodeID`：

1. 每个导出片段先分配一个挂载路径。
2. 顶层片段的挂载路径固定为 root agent name。
3. 每个可直接 invocation 的 agent 片段都对应一个真实根节点，并直接使用自己的挂载路径作为 `NodeID`。
4. graph 内部节点在所属 agent 片段挂载路径下追加 graph node ID。
5. 子 agent 片段在父节点路径下追加 child agent name，得到新的挂载路径。
6. 局部名中的路径分隔符冲突由框架内部使用确定性转义规则处理，具体算法不是公共契约的一部分。

示例：

```text
assistant
assistant/planner
assistant/team
assistant/team/researcher
```

### 6.2 SurfaceID

`SurfaceID` 在同一个 `Snapshot` 内必须唯一，并在相同结构下稳定不变。

规则固定为：

```text
<NodeID>#<SurfaceType>
```

示例：

```text
assistant#instruction
assistant/planner#model
```

### 6.3 StructureID

`StructureID` 由 canonical snapshot 做内容哈希生成。

规则如下：

1. 计算前先按 `NodeID`、`FromNodeID/ToNodeID`、`SurfaceID` 对节点、边、surface 稳定排序。
2. 移除重复边。
3. `StructureID` 不参与自身哈希。
4. 对 canonical snapshot 做稳定序列化后求哈希。
5. 最终格式使用固定前缀，例如 `struct_<hash>`。

只要静态节点、静态边、surface 或基线 surface 值发生变化，`StructureID` 就必须变化。

## 7. Surface 规则

只导出已被框架纳入统一请求级覆盖契约的 surface：

1. `instruction`
2. `global_instruction`
3. `few_shot`
4. `model`
5. `tool`
6. `skill`

导出约束如下：

1. 同一节点的同一种 `SurfaceType` 最多导出一个 `Surface`。
2. 只要该节点具备这一稳定注入面，即使当前基线值为空，也应导出对应 surface。
3. 基线文本值可为空字符串。
4. `few_shot` 导出显式基线样例集合，语义是整集合替换。
5. `model` 导出当前基线模型选择，而不是模型注册表全集。
6. `tool` 导出当前节点可见的稳定工具标识集合，语义是整集合替换。
7. `skill` 导出当前节点可见的稳定 skill 摘要集合，语义是整集合替换；当前快照至少包含稳定标识和摘要描述，供 PromptIter 做选择型优化。
8. `tool` 和 `skill` 的集合值必须稳定排序。
9. `few_shot` 必须保持配置顺序，不做重排。

以下内容不导出为 surface：

1. graph 普通 function node 的业务参数。
2. 工具声明全文、参数 schema 和实现细节。
3. skill 文档全文、运行期加载内容和中间缓存。
4. 隐式 prompt 拼接细节。
5. `ModelInstructions`、`ModelGlobalInstructions` 这类内部映射。
6. session 历史消息、one-shot messages 和运行时临时注入消息片段。

## 8. 内部导出策略

内置导出器使用私有片段模型递归拼接子结构。该内部表示不进入公共 API。

递归展开只遵守以下结果约束：

1. 只要子 agent 在静态上可达，就应递归展开。
2. 同一个 agent 实例如果挂载在多个不同父路径下，应分别展开，因为这些位置的 `NodeID` 不同。
3. 导出器必须避免在递归引用上无限展开。
4. 命中当前递归环时，当前挂载点降级成 opaque leaf node，并停止继续向下展开。

## 9. 内置运行时导出规则

### 9.1 LLMAgent

`LLMAgent` 导出一个可执行静态节点：

1. `Node.Kind = NodeKindLLM`
2. `Node.Name = agent.Info().Name`
3. `EntryNodeID = 该节点 NodeID`
4. `terminalNodeIDs = []string{该节点 NodeID}`

surface 规则：

1. 总是导出 `instruction` surface。
2. 总是导出 `global_instruction` surface。
3. 若有显式基线 few-shot 配置，则导出 `few_shot` surface。
4. 若有基线模型，则导出 `model` surface。
5. 若配置了显式 user tools 或 `ToolSets`，则导出 `tool` surface。
6. 若配置了 skills repository，则导出 `skill` surface。

若 `LLMAgent` 配置了 `SubAgents()`，则递归导出每个子 agent，并增加从当前节点到每个子片段 `EntryNodeID` 的可能边，表示 transfer/handoff 的静态可能性。

### 9.2 GraphAgent

`GraphAgent` 导出一个根 agent 节点，并在其下导出内部 graph 节点和静态边。

根节点规则：

1. `Node.Kind = NodeKindAgent`
2. `Node.Name = agent.Info().Name`
3. `NodeID = 该 graph agent 片段挂载路径`
4. `EntryNodeID = 根节点 NodeID`

graph node 规则：

1. function node 导出为 `NodeKindFunction`
2. llm node 导出为 `NodeKindLLM`
3. tool node 导出为 `NodeKindTool`
4. agent node 导出为 `NodeKindAgent`

graph edge 规则：

1. 根 agent 节点连到 graph 编译后的入口节点。
2. 普通静态边按 `From -> To` 导出。
3. conditional edge 对所有静态可解析目标导出可能边。
4. 指向 graph `End` 的边不导出为普通节点边，只参与 terminal 计算。

graph surface 规则：

1. llm node 总是导出 `instruction` surface。
2. llm node 若具备显式基线 few-shot 配置，则导出 `few_shot` surface。
3. llm node 若配置了基线模型，则导出 `model` surface。
4. llm node 若配置了工具集，则导出 `tool` surface。
5. tool node 若配置了基础工具或 `ToolSets`，则导出 `tool` surface。

graph agent node 规则：

1. graph agent node 自身仍是一个静态节点。
2. 若其引用的 child agent 支持结构导出，则递归导出子结构。
3. 增加从 graph agent node 到 child 片段 `EntryNodeID` 的可能边。

graph `terminalNodeIDs` 使用所有可能结束 graph 的静态节点集合：

1. 显式连到 `End` 的节点。
2. 没有后继边的节点。
3. conditional 路由后没有静态后继的节点。

### 9.3 ChainAgent

`ChainAgent` 导出一个根 agent 节点：

1. `Node.Kind = NodeKindAgent`
2. `Node.Name = agent.Info().Name`
3. `EntryNodeID = 根节点 NodeID`
4. 根节点连到第一个子片段 `EntryNodeID`
5. 前一个子片段的每个 `terminalNodeID` 连到下一个子片段的 `EntryNodeID`
6. 整个片段的 `terminalNodeIDs` 等于最后一个子片段的 `terminalNodeIDs`

### 9.4 ParallelAgent

`ParallelAgent` 导出一个根 agent 节点：

1. `Node.Kind = NodeKindAgent`
2. `Node.Name = agent.Info().Name`
3. `EntryNodeID = 根节点 NodeID`
4. 根节点连到每个子片段 `EntryNodeID`
5. 整个片段的 `terminalNodeIDs` 为所有子片段 `terminalNodeIDs` 的并集

### 9.5 CycleAgent

`CycleAgent` 导出一个根 agent 节点：

1. `Node.Kind = NodeKindAgent`
2. `Node.Name = agent.Info().Name`
3. `EntryNodeID = 根节点 NodeID`
4. 根节点连到本轮第一个子片段 `EntryNodeID`
5. 子片段之间按顺序连接
6. 最后一个子片段的每个 `terminalNodeID` 都回连到根节点
7. 整个片段的 `terminalNodeIDs` 保留根节点，表示循环控制点可再次触发

### 9.6 Team

Coordinator 模式：

1. 导出一个根 agent 节点。
2. 递归导出 coordinator，并以 `coordinator` 作为局部名挂到该根节点下。
3. 递归导出每个 member，并以各自 `member name` 作为局部名挂到该根节点下。
4. 根节点只连到 coordinator 片段 `EntryNodeID`。
5. coordinator 片段向每个 member 片段 `EntryNodeID` 导出静态可能边。
6. 整个 Team 的 `EntryNodeID` 使用根 agent 节点。
7. 整个 Team 的 `terminalNodeIDs` 使用 coordinator 片段的 `terminalNodeIDs`。

Swarm 模式：

1. 导出一个根 agent 节点。
2. 递归导出所有 member，并以各自 `member name` 作为局部名挂到该根节点下。
3. 根节点连到 entry member 片段的 `EntryNodeID`。
4. 为每个 member 片段的每个 `terminalNodeID` 增加到其他 member `EntryNodeID` 的可能边。
5. 整个 Team 的 `EntryNodeID` 使用根 agent 节点。
6. 整个 Team 的 `terminalNodeIDs` 为所有 member 片段 `terminalNodeIDs` 的并集。

## 10. 自定义 Agent fallback

对未实现 `structure.Exporter` 的自定义 agent，或者命中递归环而停止继续展开的挂载点，导出器降级成 opaque leaf node。

规则如下：

1. 导出一个 `NodeKindAgent` 节点。
2. `Node.Name = agent.Info().Name`
3. 不导出内部 edges。
4. 不导出内部 surfaces。
5. `EntryNodeID` 和唯一 `terminalNodeID` 都等于该节点。

## 11. 规范化与校验

`Snapshot` 返回前必须统一规范化：

1. 删除重复节点。
2. 删除重复边。
3. 删除重复 surface。
4. 校验 `EntryNodeID` 必须存在于 `Nodes`。
5. 校验所有边的 `FromNodeID` 和 `ToNodeID` 必须存在于 `Nodes`。
6. 校验所有 surface 的 `NodeID` 必须存在于 `Nodes`。
7. 校验同一 `NodeID` 下不允许重复 `SurfaceType`。
8. 对 `Nodes`、`Edges`、`Surfaces` 做稳定排序。
9. 最后计算 `StructureID`。

## 12. TDD 测试设计

本能力适合按“公共层先行、运行时逐个落地”的 TDD 方式开发。测试不以覆盖率为目标，而以锁定结构语义为目标。

### 12.1 测试分层

推荐分成三层：

1. `agent/structure` 包级测试。
   覆盖入口、能力探测、fallback、规范化、ID 规则和 `StructureID` 规则。
2. 各 built-in runtime 的结构导出测试。
   分别放在 `agent/llmagent`、`agent/graphagent`、`agent/chainagent`、`agent/parallelagent`、`agent/cycleagent`、`team` 下，覆盖各自导出语义。
3. 结构对齐集成测试。
   选择少量跨运行时组合场景，验证递归展开、挂载路径和静态可能边。

建议测试文件布局如下：

1. `agent/structure/export_test.go`
   覆盖入口、能力探测、fallback、规范化和错误路径。
2. `agent/structure/id_test.go`
   覆盖 `NodeID`、`SurfaceID`、`StructureID` 稳定性。
3. `agent/llmagent/structure_export_test.go`
4. `agent/graphagent/structure_export_test.go`
5. `agent/chainagent/structure_export_test.go`
6. `agent/parallelagent/structure_export_test.go`
7. `agent/cycleagent/structure_export_test.go`
8. `team/structure_export_test.go`

### 12.2 测试辅助约定

建议先实现以下测试辅助：

1. `assertSnapshotEqual(t, got, want)`。
   直接比较规范化后的完整 `Snapshot`，包括 `StructureID`。
2. `mustExport(t, agent)`。
   调用 `structure.Export` 并断言成功。
3. 结构断言辅助。
   至少包括 `assertNodeIDs`、`assertSurfaceTypes`、`assertEdges`。
4. 测试桩 agent。
   至少包括：
   - 不实现 `structure.Exporter` 的自定义 agent
   - 实现 `structure.Exporter` 的自定义 agent
   - 递归引用 agent
   - 名称包含路径分隔符的 agent

测试应直接断言完整结构结果，不建议只断言节点数量这类弱信号。

测试实现应沿用所在包既有断言风格；若该包此前没有明确偏好，优先使用 `assert`/`require` 对完整结果做结构化断言，不使用弱化语义的字符串包含断言代替结构断言。

### 12.3 第一批必须先写的公共测试

1. `TestExport_NilAgent_ReturnsError`
   传入 `nil`，断言返回错误。
2. `TestExport_CustomExporter_UsesExporter`
   自定义 agent 实现 `structure.Exporter`，断言走自定义导出结果。
3. `TestExport_CustomAgent_FallsBackToOpaqueLeaf`
   自定义 agent 未实现 `structure.Exporter`，断言导出为单节点 opaque leaf。
4. `TestExport_NormalizesAndSortsSnapshot`
   自定义 exporter 返回乱序、重复节点/边/surface，断言返回结果被稳定排序并去重。
5. `TestExport_RejectsMissingEntryNode`
   自定义 exporter 返回不存在的 `EntryNodeID`，断言报错。
6. `TestExport_RejectsMissingEdgeEndpoint`
   自定义 exporter 返回指向不存在节点的边，断言报错。
7. `TestExport_RejectsMissingSurfaceNode`
   自定义 exporter 返回挂到不存在节点的 surface，断言报错。
8. `TestExport_RejectsDuplicateSurfaceTypeOnSameNode`
   同一 `NodeID` 下重复导出相同 `SurfaceType`，断言报错。

### 12.4 ID 与稳定性测试

1. `TestExport_NodeID_IsStableAcrossRepeatedExports`
   相同结构重复导出，断言 `NodeID` 不变。
2. `TestExport_SurfaceID_IsStableAcrossRepeatedExports`
   相同结构重复导出，断言 `SurfaceID` 不变。
3. `TestExport_StructureID_IsStableAcrossRepeatedExports`
   相同结构重复导出，断言 `StructureID` 不变。
4. `TestExport_StructureID_ChangesWhenNodeChanges`
   改变节点集合，断言 `StructureID` 变化。
5. `TestExport_StructureID_ChangesWhenEdgeChanges`
   改变静态边，断言 `StructureID` 变化。
6. `TestExport_StructureID_ChangesWhenSurfaceValueChanges`
   改变 surface 基线值，断言 `StructureID` 变化。
7. `TestExport_NodeID_EscapesConflictingLocalName`
   agent name 或 child name 含路径分隔符，断言导出结果唯一且稳定。

### 12.5 LLMAgent 测试

1. `TestExport_LLMAgent_Basic`
   断言导出单个 `NodeKindLLM` 节点，`EntryNodeID` 为该节点。
2. `TestExport_LLMAgent_AlwaysExportsInstructionAndGlobalInstruction`
   即使基线值为空，也断言存在这两个 surface。
3. `TestExport_LLMAgent_ExportsFewShotWhenConfigured`
   配置 few-shot，断言导出 `few_shot` surface 且保持顺序。
4. `TestExport_LLMAgent_ExportsModelWhenConfigured`
   配置模型，断言导出 `model` surface。
5. `TestExport_LLMAgent_ExportsToolSet`
   配置 tools 或 `ToolSets`，断言导出稳定排序后的 `tool` surface。
6. `TestExport_LLMAgent_ExportsSkillSet`
   配置 skills repository，断言导出稳定排序后的 `skill` surface。
7. `TestExport_LLMAgent_SubAgentsCreateStaticPossibleEdges`
   配置 `SubAgents()`，断言从当前节点到各子结构入口存在静态可能边。

### 12.6 GraphAgent 测试

1. `TestExport_GraphAgent_HasRootAgentNode`
   断言存在 graph 根 agent 节点，`EntryNodeID` 为根节点。
2. `TestExport_GraphAgent_ExportsCompiledEntryEdge`
   断言根节点连到 graph 编译后的入口节点。
3. `TestExport_GraphAgent_FunctionNodeKind`
   function node 导出为 `NodeKindFunction`。
4. `TestExport_GraphAgent_LLMNodeKindsAndSurfaces`
   llm node 导出为 `NodeKindLLM`，并按配置导出 `instruction/few_shot/model/tool`。
5. `TestExport_GraphAgent_ToolNodeSurface`
   tool node 配置工具集时导出 `tool` surface。
6. `TestExport_GraphAgent_AgentNodeRecursesIntoChild`
   graph agent node 引用 child agent 时，断言递归导出子结构并建立可能边。
7. `TestExport_GraphAgent_ConditionalEdgesExportPossibleTargets`
   conditional edge 的所有静态可解析目标都被导出为可能边。
8. `TestExport_GraphAgent_EndEdgesAffectTerminalsOnly`
   指向 `End` 的边不作为普通边导出，但会影响 `terminalNodeIDs`。

### 12.7 Chain/Parallel/Cycle 测试

1. `TestExport_ChainAgent_RootConnectsToFirstChild`
2. `TestExport_ChainAgent_SequentialEdgesFollowTerminalToEntry`
3. `TestExport_ChainAgent_TerminalsEqualLastChildTerminals`
4. `TestExport_ParallelAgent_RootConnectsToAllChildren`
5. `TestExport_ParallelAgent_TerminalsUnionAllChildren`
6. `TestExport_CycleAgent_RootConnectsToFirstChild`
7. `TestExport_CycleAgent_LastChildReconnectsToRoot`
8. `TestExport_CycleAgent_TerminalsIncludeRoot`

这些测试应使用至少一个 child 为多 terminal 子结构的场景，避免只验证最简单线性子树。

### 12.8 Team/Swarm 测试

1. `TestExport_TeamCoordinator_RootConnectsOnlyToCoordinator`
2. `TestExport_TeamCoordinator_CoordinatorHasPossibleEdgesToMembers`
3. `TestExport_TeamCoordinator_TerminalsFollowCoordinator`
4. `TestExport_Swarm_RootConnectsToEntryMember`
5. `TestExport_Swarm_TransferRosterExportsMemberToMemberPossibleEdges`
6. `TestExport_Swarm_TerminalsUnionAllMembers`
7. `TestExport_Swarm_UpdateRosterReflectsInSnapshot`
   若运行时支持 roster 更新后的静态读取，应断言导出结果随当前 roster 变化。

### 12.9 递归与 fallback 测试

1. `TestExport_ReusedSameAgentInstanceUnderDifferentParents_ExpandsTwiceWithDifferentNodeIDs`
2. `TestExport_RecursiveReference_FallsBackToOpaqueLeafAtCyclePoint`
3. `TestExport_CustomExporterInsideBuiltInRuntime_IsUsedRecursively`
4. `TestExport_CustomNonExporterInsideBuiltInRuntime_FallsBackRecursively`

### 12.10 推荐 TDD 顺序

推荐按以下顺序写测试并实现：

1. 公共入口、规范化、fallback、`StructureID`。
2. `LLMAgent`。
3. `GraphAgent`。
4. `ChainAgent`、`ParallelAgent`、`CycleAgent`。
5. `Team/Swarm`。
6. 递归引用、复用实例、路径冲突。

每完成一类运行时，就先把该类测试补齐，再进入下一类。

## 13. 验收标准

实现完成后应满足以下验收标准：

1. 相同 agent 系统重复导出时，`NodeID`、`SurfaceID` 和 `StructureID` 保持稳定。
2. 修改 graph 节点、静态边或 surface 基线值后，`StructureID` 会变化。
3. graph 内部的 llm node 能稳定导出 `instruction` 和 `model` surface。
4. 每个可直接 invocation 的 built-in agent 片段都会出现在结构图中，并拥有稳定 `NodeID`。
5. `Team/Swarm` 能把 member 关系导出成静态可能边。
6. 自定义 agent 实现 `structure.Exporter` 时能够深度导出；未实现时也能以 opaque leaf 形式出现在结构图中。
7. Execution Trace 后续可直接复用这套 `NodeID` 规则，不需要再发明第二套节点身份体系。

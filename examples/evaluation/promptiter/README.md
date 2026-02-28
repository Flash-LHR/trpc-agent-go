# PromptIter Closed-Loop Example (Sportscaster)

This example demonstrates a minimal, end-to-end **prompt iteration loop**:

1. **Candidate inference**: generate a Chinese sports report from a JSON “match state”.
2. **Evaluation**:
   - `json_schema_valid`: deterministic JSON Schema validation.
   - `llm_rubric_critic`: an LLM judge with a rubric that emits `issues[]`.
3. **Gradient aggregation**: deduplicate issues into an aggregated gradient JSON.
4. **Prompt optimization**: an optimizer agent edits `prompt_after.md` precisely via `tool/file`.
5. **Iterate** for up to `-iters` rounds, or stop early when all metrics pass.

The optimizer is sandboxed to the `output/` directory and only edits `output/iter_XXXX/prompt_after.md` (not the source prompt under `prompts/`).

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENAI_API_KEY` | API key for OpenAI-compatible models (teacher/aggregator/optimizer/judge) | `` |
| `OPENAI_BASE_URL` | OpenAI-compatible base URL | `https://api.openai.com/v1` |

Notes:

- All loop agents use the `openai` provider and will read `OPENAI_API_KEY` / `OPENAI_BASE_URL`.
- The `llm_rubric_critic` judge model is configured in the metrics JSON with `${OPENAI_API_KEY}` / `${OPENAI_BASE_URL}` placeholders.

## Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-app` | App name used to locate evalsets/metrics under `-data-dir` | `sportscaster_eval_app` |
| `-evalset` | Eval set id to execute (repeatable or comma-separated); omit to run all evalsets under app | `` |
| `-data-dir` | Directory containing `*.evalset.json` and `*.metrics.json` files | `./data` |
| `-out-dir` | Directory to store iteration artifacts | `./output` |
| `-schema` | Output JSON schema path | `./schemas/output_schema.json` |
| `-iters` | Max iteration rounds | `3` |
| `-candidate-model` | Candidate model name | `deepseek-v3.2` |
| `-teacher-model` | Teacher model name | `gpt-5.2` |

## Run

```bash
cd trpc-agent-go/examples/evaluation/promptiter

export OPENAI_API_KEY="..."

go run . -iters 3
```

Run a specific evalset:

```bash
go run . -evalset sportscaster_basic -iters 3
```

Run multiple evalsets:

```bash
go run . -evalset sportscaster_basic -evalset other_evalset
```

Comma-separated ids are also supported:

```bash
go run . -evalset sportscaster_basic,other_evalset
```

If `-evalset` is omitted, the example runs **all** evalsets under `data/<app>/`.

## Data Layout

```text
promptiter/
  data/
    sportscaster_eval_app/
      sportscaster_basic.evalset.json
      sportscaster_basic.metrics.json
  schemas/
    output_schema.json
    judge_output_schema.json
    aggregated_gradient_schema.json
  prompts/
    target/
      target_prompt_v1_0.md
    teacher.md
    judge_critic.md
    gradient_aggregator.md
    optimizer.md
```

In this example, each eval case `userContent.content` is a **JSON string** representing the match state. The candidate agent is expected to produce a JSON report that conforms to `schemas/output_schema.json`.

## Prompt Format

The optimized prompt (`prompts/target/target_prompt_v1_0.md`) is a Markdown document, typically split into sections:

```md
## role
...

## output_contract
...
```

The optimizer is instructed to keep `## <section_id>` headings stable and only edit section bodies.

## Output

Iteration artifacts are written under `-out-dir`:

```text
output/
  iter_0001/
    prompt_before.md
    prompt_after.md
    aggregated_gradient.json
    optimizer_changes.json
  iter_0002/
  ...
  evalresult/
    <app_name>/
      <eval_set_result_id>.evalset_result.json
```

- `aggregated_gradient.json`: aggregated `issues[]` (deduplicated prompt issues) plus optional `notes` for the optimizer.
- `optimizer_changes.json`: optimizer change metadata (currently only `changed_sections`, reserved for future diffs).
- `evalresult/<app_name>/<eval_set_result_id>.evalset_result.json`: raw `EvalSetResult` produced by `evaluation.AgentEvaluator` (includes per-case traces and per-metric details).

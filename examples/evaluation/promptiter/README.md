# PromptIter Closed-Loop Example (Sportscaster)

This example demonstrates a minimal, end-to-end **prompt iteration loop**:

1. **Candidate inference**: generate a Chinese sports report from a JSON “match state”.
2. **Evaluation**:
   - `json_schema_valid`: deterministic JSON Schema validation.
   - `llm_rubric_critic`: an LLM judge with a rubric that emits `issues[]`.
3. **Gradient aggregation**: deduplicate and map issues to prompt sections.
4. **Prompt optimization**: an optimizer agent edits `prompt.md` precisely via `tool/file`.
5. **Iterate** for up to `-iters` rounds, or stop early when all metrics pass.

The optimizer is sandboxed to the `output/` directory and only edits `output/iter_XXXX/prompt.md` (not the source prompt under `prompts/`).

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DEEPSEEK_API_KEY` | API key for DeepSeek (used when `-candidate-model` starts with `deepseek`) | `` |
| `DEEPSEEK_BASE_URL` | DeepSeek OpenAI-compatible base URL | `https://api.deepseek.com` |
| `OPENAI_API_KEY` | API key for OpenAI-compatible models (teacher/aggregator/optimizer/judge) | `` |
| `OPENAI_BASE_URL` | OpenAI-compatible base URL | `https://api.openai.com/v1` |

Notes:

- By default, the candidate model is `deepseek-v3.2` (via the `openai` provider) and will read `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL`.
- The teacher/aggregator/optimizer models default to `gpt-5.2` and will read `OPENAI_API_KEY` / `OPENAI_BASE_URL`.
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

export DEEPSEEK_API_KEY="..."
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
  prompts/
    target/
      target_prompt_v1_0.md
    teacher.md
    judge_critic.md
    gradient_aggregator.md
    optimizer.md
```

In this example, each eval case `userContent.content` is a **JSON string** representing the match state. The loop pre-validates that it can be parsed into a JSON object.

## Prompt Format

The optimized prompt (`prompts/target/target_prompt_v1_0.md`) is a Markdown document split into stable sections:

```md
## role
...

## output_contract
...
```

The optimizer is required to keep section ids stable (the loop validates this) and only edit section bodies.

## Output

Iteration artifacts are written under `-out-dir`:

```text
output/
  iter_0001/
    prompt_before.md
    prompt.md
    prompt_after.md
    evalsets/
      <evalset_id>/
        evalset_result.json
    aggregated_gradient.json
    optimizer_changes.json
  iter_0002/
  ...
```

- `aggregated_gradient.json`: aggregated `issues[]` plus a `by_section` mapping that points to the target prompt sections.
- `optimizer_changes.json`: section ids changed by the optimizer (derived from a before/after diff).

# PromptIter SyncRun Example

This example runs PromptIter end to end on a real sports live-commentary generation task through `engine.Run`.

The candidate agent is a single `llmagent` with a deliberately simple instruction. PromptIter directly optimizes that one instruction so the example highlights instruction tuning instead of relying on a heavy graph scaffold. The initial seed should stay simple on purpose, because a strong hand-written starting instruction weakens the demonstration value of PromptIter itself.

## Data Files

The example loads these files from `./data/promptiter-nba-commentary-app/` by default:

- `nba-commentary-train.evalset.json`
- `nba-commentary-validation.evalset.json`
- `sports-commentary.metrics.json`

The train and validation sets are generated directly from a real sports-business `jsonl` file, but each case now stores a structured JSON snapshot of the live game state instead of the original long prompt. The JSON input keeps the current event, live score context, recent commentary, recent events, and on-court lineup. Global output expectations such as live-call style and concise length are enforced by the shared metric file and optimized instruction rather than being embedded inside every eval-case input. Every eval case also enables `expectedRunnerEnabled`, so the example can generate teacher reference answers dynamically during evaluation.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the OpenAI-compatible endpoint | `` |
| `OPENAI_BASE_URL` | Base URL for the OpenAI-compatible endpoint | `https://api.openai.com/v1` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-data-dir` | Directory containing evaluation set and metric files | `./data` |
| `-output-dir` | Directory where evaluation results will be stored | `./output` |
| `-model` | Model identifier used by the candidate agent | `deepseek-v3-local-II` |
| `-judge-model` | Model identifier used by the judge agent | `gpt-5.2` |
| `-worker-model` | Model identifier used by the PromptIter worker agent | `gpt-5.4` |
| `-teacher-temperature` | Temperature used by the teacher agent that generates reference commentary | `0.2` |
| `-runs` | Number of evaluation repetitions per case | `1` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |
| `-min-score-gain` | Minimum validation score gain required to accept a patch | `0.005` |
| `-max-rounds-without-acceptance` | Maximum consecutive rejected rounds before stopping | `5` |
| `-target-score` | Target validation score that stops optimization when reached | `1.0` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `8` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |
| `-debug-io` | Log candidate, teacher, judge, and PromptIter worker inputs and outputs | `false` |
| `-experiment-config` | Optional JSON file that defines A/B test or ablation variants | `` |
| `-experiment-summary` | Optional JSON file where experiment summary will be written | auto-generated under `./output/experiments/.../summary.json` |

## Run

```bash
cd examples/evaluation/promptiter/syncrun
export OPENAI_BASE_URL="http://v2.open.venus.oa.com/llmproxy/"
export OPENAI_API_KEY="***"
go run . \
  -model "deepseek-v3-local-II" \
  -judge-model "gpt-5.2" \
  -worker-model "gpt-5.4"
```

For deep troubleshooting, enable component IO logs:

```bash
go run . -debug-io=true
```

## A/B Tests And Ablations

The syncrun example can run repeated A/B tests or ablations without changing framework defaults or binding PromptIter worker prompts to this sports task. The built-in experiment harness reuses the same `engine.Run` path and only varies explicit run settings that you choose in a JSON file.

Use the included sample config:

```bash
go run . \
  -experiment-config ./ablation.example.json
```

Or write your own config with multiple named variants:

```json
{
  "trials": 3,
  "variants": [
    {
      "name": "control"
    },
    {
      "name": "teacher-temp-0",
      "teacher_temperature": 0.0
    },
    {
      "name": "max-rounds-2",
      "max_rounds": 2
    },
    {
      "name": "teacher-temp-0-max-rounds-2",
      "teacher_temperature": 0.0,
      "max_rounds": 2
    }
  ]
}
```

Each variant inherits the regular CLI config, then overrides only the fields you specify. The experiment summary reports repeated-trial statistics such as mean, median, standard deviation, accepted round distribution, and per-trial score deltas. This keeps the comparison scientific and avoids task-specific trick tuning inside `backwarder`, `aggregator`, or `optimizer`.

For the current `0.98` push, the most useful first comparison is usually:

- `control`
- `teacher-temp-0`
- `max-rounds-2`
- `teacher-temp-0-max-rounds-2`

This keeps the experiment small, targets the two main observed failure sources, and avoids searching a large hyperparameter grid.

The default settings enable parallel evaluation for throughput. If you use the public llmproxy endpoint, you may need to lower parallelism or disable parallel flags when the service returns rate-limit errors.

The syncrun example uses:

- `candidate=deepseek-v3-local-II`
- `teacher=gpt-5.2`
- `judge=gpt-5.2`
- `worker=gpt-5.4`

## Verified Result

With the recommended model split:

- `candidate=deepseek-v3-local-II`
- `teacher=gpt-5.2`
- `judge=gpt-5.2`
- `worker=gpt-5.4`

one verified run improved validation from roughly `0.56` at baseline to roughly `0.95` after the first accepted PromptIter round on the same train/validation data.

## What It Does

- Loads train and validation eval sets from local files.
- Evaluates the candidate on structured sports game-state JSON inputs.
- Enables parallel inference and parallel evaluation across eval cases by default for faster end-to-end runs.
- Uses a dedicated teacher runner to generate dynamic expected commentary references.
- Uses one expected-aware LLM rubric metric, one deterministic final-response length evaluator, and one judge-based rubric metric.
- Keeps the default `backwarder`, `aggregator`, and `optimizer` prompts generic; the example does not hard-code sports-specific worker prompts.
- Directly targets the known `candidate#instruction` surface in `TargetSurfaceIDs`.
- Optimizes only that single candidate `instruction` surface via `TargetSurfaceIDs`.
- Prints the initial instruction, accepted instruction, and score changes.
- Writes raw evaluation results under `./output`.

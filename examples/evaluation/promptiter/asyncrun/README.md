# PromptIter AsyncRun Example

This example runs PromptIter end to end through `manager.Start` and `manager.Get`.

The candidate agent is a single `llmagent` with a deliberately simple instruction. PromptIter directly optimizes that one instruction so the example highlights asynchronous run lifecycle management instead of HTTP transport.

## Data Files

The example loads these files from `./data/promptiter-nba-commentary-app/` by default:

- `nba-commentary-train.evalset.json`
- `nba-commentary-validation.evalset.json`
- `sports-commentary.metrics.json`

The train and validation sets are generated directly from a real sports-business `jsonl` file, but each case stores a structured JSON snapshot of the live game state instead of the original long prompt. The JSON input keeps the current event, live score context, recent commentary, recent events, and on-court lineup. Global output expectations such as live-call style and concise length are enforced by the shared metric file and optimized instruction rather than being embedded inside every eval-case input. Every eval case also enables `expectedRunnerEnabled`, so the example can generate teacher reference answers dynamically during evaluation.

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
| `-runs` | Number of evaluation repetitions per case | `1` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `8` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |
| `-poll-interval` | Polling interval used to wait for asynchronous run completion | `1s` |
| `-debug-io` | Log candidate, teacher, judge, and PromptIter worker inputs and outputs | `false` |

## Run

```bash
cd examples/evaluation/promptiter/asyncrun
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

The default settings enable parallel evaluation for throughput. If you use the public llmproxy endpoint, you may need to lower parallelism or disable parallel flags when the service returns rate-limit errors.

## What It Does

- Builds the same PromptIter runtime as `syncrun`.
- Starts an asynchronous run through `manager.Start`.
- Polls run state through `manager.Get` until the run reaches a terminal state.
- Prints the run ID, accepted instruction, and score changes after completion.
- Writes raw evaluation results under `./output`.

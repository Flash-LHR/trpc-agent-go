# PromptIter Server Example

This example exposes the sports-commentary PromptIter workflow as an HTTP control-plane service.

It serves PromptIter through [server/promptiter](/cbs/workspace/external-trpc-agent-go/promptiter-docs/trpc-agent-go/server/promptiter).
The candidate app behind the server is a single `llmagent` with a deliberately simple summary-style instruction. PromptIter optimizes that one instruction directly.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the OpenAI-compatible endpoint | `` |
| `OPENAI_BASE_URL` | Base URL for the OpenAI-compatible endpoint | `https://api.openai.com/v1` |
| `MODEL_NAME` | Model used by the candidate, teacher, judge, and PromptIter worker agents | `gpt-5.2` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-addr` | Listen address for the PromptIter server | `:8080` |
| `-base-path` | Base path exposed by the PromptIter server | `/promptiter/v1/apps` |
| `-data-dir` | Directory containing evaluation set and metric files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-model` | Model identifier used by the example | `$MODEL_NAME` or `gpt-5.2` |
| `-runs` | Number of evaluation repetitions per case | `1` |
| `-eval-case-parallelism` | Maximum number of eval cases processed in parallel | `8` |
| `-parallel-inference` | Enable parallel inference across eval cases | `true` |
| `-parallel-evaluation` | Enable parallel evaluation across eval cases | `true` |
| `-debug-io` | Log candidate, teacher, judge, and PromptIter worker inputs and outputs | `false` |

## Data Files

The server example keeps its own data under `./data/promptiter-nba-commentary-app/`:

- `nba-commentary-train.evalset.json`
- `nba-commentary-validation.evalset.json`
- `sports-commentary.metrics.json`

The eval sets are generated from the same sports-business source data as the syncrun example, but every case stores a structured JSON snapshot of the live game state instead of the original long prompt. The JSON input keeps the current event, live score context, recent commentary, recent events, and on-court lineup. Global output expectations such as concise length and spoken live-call style are enforced by the shared metric file and optimized instruction instead of being embedded inside each sample. Every eval case also enables `expectedRunnerEnabled`, so the server runtime generates teacher references dynamically during evaluation.

## Run

```bash
cd examples/evaluation/promptiter/server
export OPENAI_BASE_URL="http://v2.open.venus.oa.com/llmproxy/"
export OPENAI_API_KEY="***"
export MODEL_NAME="gpt-5.2"
go run . \
  -addr ":8080" \
  -base-path "/promptiter/v1/apps" \
  -data-dir "./data" \
  -output-dir "./output"
```

For deep troubleshooting, enable component IO logs:

```bash
go run . -debug-io=true
```

The default settings enable parallel evaluation for throughput. If you use the public llmproxy endpoint, you may need to lower parallelism or disable parallel flags when the service returns rate-limit errors.

The shared metric file combines one expected-aware LLM rubric metric, one deterministic final-response length evaluator, and one judge-based rubric metric. This keeps the concise-output objective while also using a dedicated teacher runner and an LLM judge for commentary quality.

The server exposes:

- `GET /promptiter/v1/apps/promptiter-nba-commentary-app/structure`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/runs`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs`
- `GET /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/{run_id}`
- `POST /promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/{run_id}/cancel`

## Example Requests

Fetch the current editable structure:

```bash
curl "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/structure"
```

Use the returned structure to find the editable `candidate` instruction surface. The example currently resolves to `candidate#instruction`, but callers should treat that value as structure-derived instead of hard-coding the name.

Run one PromptIter optimization session:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/runs" \
  -H "Content-Type: application/json" \
  -d '{
    "run": {
      "TrainEvalSetIDs": ["nba-commentary-train"],
      "ValidationEvalSetIDs": ["nba-commentary-validation"],
      "TargetSurfaceIDs": ["candidate#instruction"],
        "EvaluationOptions": {
          "NumRuns": 1,
          "EvalCaseParallelism": 8,
          "EvalCaseParallelInferenceEnabled": true,
          "EvalCaseParallelEvaluationEnabled": true
        },
      "AcceptancePolicy": {
        "MinScoreGain": 0.01
      },
      "StopPolicy": {
        "MaxRoundsWithoutAcceptance": 3,
        "TargetScore": 1
      },
      "MaxRounds": 4
    }
  }'
```

The `runs` endpoint waits until the run reaches a terminal state and then returns the full `run` result directly.

Run one asynchronous PromptIter optimization session:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs" \
  -H "Content-Type: application/json" \
  -d '{
    "run": {
      "TrainEvalSetIDs": ["nba-commentary-train"],
      "ValidationEvalSetIDs": ["nba-commentary-validation"],
      "TargetSurfaceIDs": ["candidate#instruction"],
        "EvaluationOptions": {
          "NumRuns": 1,
          "EvalCaseParallelism": 8,
          "EvalCaseParallelInferenceEnabled": true,
          "EvalCaseParallelEvaluationEnabled": true
        },
      "AcceptancePolicy": {
        "MinScoreGain": 0.01
      },
      "StopPolicy": {
        "MaxRoundsWithoutAcceptance": 3,
        "TargetScore": 1
      },
      "MaxRounds": 4
    }
  }'
```

The asynchronous endpoint immediately returns a persisted asynchronous `run` view. Use the returned `run.ID` to query lifecycle state and round details:

```bash
curl "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/<run_id>"
```

The run detail response contains:

- run status and current round
- baseline validation result and score
- per-round train and validation scores
- per-round losses, backward results, aggregation, patches, output profiles, acceptance, and stop decisions
- final accepted profile when the run succeeds

Cancel one asynchronous run:

```bash
curl -X POST "http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app/async-runs/<run_id>/cancel"
```

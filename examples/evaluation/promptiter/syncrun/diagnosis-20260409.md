# PromptIter Syncrun Upper-Bound Diagnosis

## Scope

This note records the upper-bound diagnosis for the NBA commentary PromptIter example on 2026-04-09.

The diagnosis used these result directories:

- `/tmp/promptiter-98-teacher-v2-1`
- `/tmp/promptiter-98-teacher-v2-2`
- `/tmp/promptiter-98-fast-1`
- `/tmp/promptiter-98-fast-2`
- `/tmp/promptiter-98-climb-1`
- `/tmp/promptiter-98-t01-1`

The goal was to determine whether the current setup can stably reach `0.98+` without trick optimization and without making the generic PromptIter worker prompts business-specific.

## Current Best Observed Result

The best observed validation result is still:

- run: `/tmp/promptiter-98-teacher-v2-1`
- file: `/tmp/promptiter-98-teacher-v2-1/promptiter-nba-commentary-app/promptiter-nba-commentary-app_nba-commentary-validation_f5c007e8-01d0-42f6-8c9e-0ca60f1e1159.evalset_result.json`
- exact score: `0.9740740740740742`

Metric breakdown for that best result:

- `final_response_length_compliance = 0.9777777777777777`
- `llm_rubric_expected_response = 0.9555555555555556`
- `llm_rubric_response = 0.9888888888888889`

Gap to `0.98`:

- `0.98 - 0.9740740740740742 = 0.0059259259259258`

This means the setup is already very close to `0.98`, but the remaining gap is not being crossed stably by repeated reruns.

## What Repeated Reruns Showed

Recent repeats with the same general instruction family did not reproduce the `0.974+` result:

- `/tmp/promptiter-98-fast-1`: best accepted validation `0.9345679012345679`
- `/tmp/promptiter-98-fast-2`: best intermediate validation `0.9166666666666666`
- `/tmp/promptiter-98-climb-1`: best intermediate validation `0.9314814814814815`
- `/tmp/promptiter-98-t01-1`: best intermediate validation `0.9234567901234567`

Two small parameter changes were also not helpful:

- reducing `teacher_temperature` from `0.2` to `0.1` did not improve the ceiling
- reducing `min_score_gain` from `0.005` to `0.001` did not produce useful upward crawl

The repeated evidence suggests that the current setup can occasionally touch the high `0.97` range, but it does not currently support a stable `0.98+` outcome.

## Where The Best Run Still Loses Points

In the best run, only seven validation cases contribute all remaining score loss.

Per-case non-perfect metric entries:

- `case_10`: `llm_rubric_expected_response = 0.6666666666666666`
- `case_10`: `llm_rubric_response = 0.75`
- `case_24`: `final_response_length_compliance = 0.5`
- `case_31`: `llm_rubric_expected_response = 0.6666666666666666`
- `case_31`: `llm_rubric_response = 0.75`
- `case_33`: `final_response_length_compliance = 0.5`
- `case_39`: `llm_rubric_expected_response = 0.3333333333333333`
- `case_48`: `llm_rubric_expected_response = 0.6666666666666666`
- `case_6`: `llm_rubric_expected_response = 0.6666666666666666`

This is important because the problem is not broad quality collapse. The problem is concentrated in a very small number of edge cases.

## Failure Pattern 1: Wrong Actor Attribution

The biggest repeated failure mode is unsupported actor assignment.

Examples:

- `case_10`
  - actual: `霍勒迪在第四节最后1分02秒抢下关键篮板...`
  - expected: `申京第一罚没进但火箭自己抢下篮板继续二次进攻！`
  - issue: the JSON only says the Rockets got the rebound. It does not say Holiday got it.

- `case_31`
  - actual: `霍勒迪在第四节还剩2分54秒时抢下关键篮板...`
  - expected: `德罗赞第二罚没进，火箭抢下篮板但仍以92比102落后...`
  - issue: the answer invents Holiday as the rebounder.

- `case_6`
  - actual: `雷诺在申京头上硬生生抢下进攻篮板...`
  - expected: `德罗赞跳投被霍勒迪封掉没进，雷诺立刻冲上来抢下进攻篮板...`
  - issue: the answer keeps the rebound but drops the decisive preceding blocked-shot cue from the reference.

The key point is that these failures are not about broad commentary quality. They are about over-specific attribution when the input only supports a team-level event.

## Failure Pattern 2: Teacher Alignment Expects The Immediate Event Chain

Several `llm_rubric_expected_response` failures come from the teacher reference preserving a short causal chain from `recent_events`, not just `current_event`.

Examples:

- `case_10`
  - reference keeps: `申京第一罚没进 -> 火箭抢到篮板 -> 二次进攻`
  - actual keeps only the rebound moment and also invents an actor

- `case_31`
  - reference keeps: `德罗赞第二罚没进 -> 火箭抢下篮板`
  - actual keeps only the rebound and invents an actor

- `case_48`
  - reference keeps: `申京两罚全丢 -> 国王喊暂停`
  - actual keeps the timeout and score but drops the immediate missed-free-throw cue

- `case_39`
  - reference keeps the substitution as a direct post-timeout action
  - actual weakens this into a more generic defensive intention

This means the current teacher-alignment metric is not merely asking for "comment on the current event." In a subset of cases it rewards preserving the decisive immediate chain implied by `recent_events`.

That is a legitimate evaluation choice, but it materially raises the ceiling difficulty.

## Failure Pattern 3: Length Failures Are Tiny, Not Structural

The two length failures are:

- `case_24`: actual length `60`, only `2` runes over the preferred upper bound
- `case_33`: actual length `61`, only `3` runes over the preferred upper bound

These are not structural verbosity failures. They are near-miss cases caused by one extra clause.

This matters because the length metric is not the dominant bottleneck. It is almost fully solved already.

## Why The Setup Is Close To 0.98 But Not There Yet

To exceed `0.98`, the best run needs a total metric gain of only:

- `0.0177777777777774` across the three metric averages

That sounds small, but the remaining misses are concentrated in hard edge cases.

One unsupported-actor fix on a case like `case_10` would increase the final run score by about:

- `0.00432099`

One tiny length fix on a case like `case_24` would increase the final run score by about:

- `0.0037037`

Together, those two changes alone would push the best run from `0.9740740740740742` to about:

- `0.982099`

So the ceiling is not far away. The real issue is that the system is not hitting those exact edge-case fixes reliably.

## Diagnosis

The current ceiling problem is not caused by:

- generic PromptIter worker prompts
- the backwarder/aggregator/optimizer protocol
- train versus validation repeat counts
- broad failure to produce live commentary

The dominant bottleneck is:

- unstable handling of ambiguous actor attribution
- unstable preservation of the teacher's immediate event chain in a handful of cases

In other words, the remaining gap to `0.98+` is mostly a precision problem, not a general-quality problem.

## Reasonable Next Steps

The most reasonable next optimization direction is to improve the example-side candidate behavior, not the generic PromptIter worker prompts.

The candidate behavior that would most directly target the remaining gap is:

1. When `current_event` or `recent_events` do not explicitly identify the rebounder, fouler, or substitution actor, do not assign a named player.
2. When the latest `recent_events` clearly form an immediate decisive chain, prefer preserving that chain in one sentence instead of collapsing to only the terminal event.
3. Keep the line inside the current preferred length range by trimming one extra clause in timeout or missed-shot cases.

If future work still cannot make these edge cases reliable, the next thing to audit is not PromptIter itself, but whether the current `llm_rubric_expected_response` reference policy is too strict for the stability target of `0.98+`.

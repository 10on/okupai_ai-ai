---
name: local-offload
description: Use at the start of any multi-step task, and whenever a step looks token-heavy, bulky, repetitive or non-urgent (browsing, reading large files/logs, transcription, boilerplate, mass edits). Decides whether to offload work to the local LLM on the Mac and plans the batching.
---

# Step 0 — mode check (every local-* skill starts here)

Call `local_status`. If only `local_enable` is available, or the mode is `off`, local mode is
**disabled — do NOT enable it yourself**. Tell the user in one line what you would offload and why,
suggest `/local`, and continue in the cloud if they don't take it up.

| Mode | Cloud verifies local output? | Mac unavailable / local fails twice |
|---|---|---|
| `prefer` | yes — local output is a draft | do it in the cloud, say so |
| `strict` | no — accept as is, mark **unverified** (code still must pass tests before commit) | `local_submit` the work or stop and report; never take over |

Check `local_capabilities` before assuming a profile, service (whisper, browser) or template exists.

# When to offload

Offload when at least one is true:
- Large input, simple operation: summarize/extract/classify big docs, logs, pages.
- Many small similar operations (batch transforms, renames, translations, data cleanup).
- Browser work with many page views.
- Audio/video: transcription, subtitles.
- Non-urgent, well-specified, easily verifiable task.

Keep in the cloud (even in strict mode, tell the user instead of offloading):
- Architecture, design decisions, tricky debugging, security-sensitive code.
- Anything urgent where model load/switch time dominates.
- Tasks where verifying the result costs as much as doing it.

# Planning

1. Split the task; tag each subtask with a profile: `fast` | `coding` | `browser` | `brain` | `heavy`
   (or service `whisper`). See `local_capabilities` for what each is and whether it's downloaded.
2. Only one model is loaded at a time. Group subtasks by profile and order them to minimise switches,
   cheapest-to-load first unless dependencies force otherwise. `brain`/`heavy` load in minutes.
3. Show the user the plan in one or two lines before starting long local work.
4. Use `template` + `vars` in `local_run` where a template fits — fewer cloud tokens, steadier output.
5. Long or non-urgent batches → `local_submit` (async; the NAS worker groups by profile).
6. After each batch (prefer): verify — spot-check, tests, diff review. Poor local quality twice in a
   row → do it in the cloud and say so.
7. Record the outcome with `local_feedback` (accepted / fixed / redone / unverified).
8. Report in one line: what ran locally, which profile, how long, verification result.

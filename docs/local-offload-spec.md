# Local-LLM offload for NAS agents — spec (as built)

Replaces the draft notes 01–06 (MCP spec, skills, revised browser skill, models/context/async,
open questions, local modes). Status markers: **built** = implemented and tested end-to-end from the
NAS against the Mac; **planned** = not implemented.

## 1. Goal

Claude Code and Codex run on the NAS. By default they never use the Mac's local LLM and don't learn
about it beyond the skill descriptions. The user switches local mode on mid-session (`/local`), after
a confirmation; from then on the agent can offload bulky, repetitive or non-urgent work to the Mac.

## 2. Components

| Component | Where | Status |
|---|---|---|
| `aimode` CLI — model lifecycle on the Mac | `mac-ai-mode/` | built |
| `aimode-mcp` — stdio MCP server, one process per agent session | `nas-agents/aimode-mcp` | built |
| `aimode-mcp worker` — async job queue, one per NAS (systemd --user) | same binary | built |
| Browser agent for `local_browse` | `nas-agents/browser/agent.mjs` | built |
| Prompt templates | `nas-agents/templates/<profile>/<task>.md` | built |
| `/local` command (Claude Code; Codex custom prompt) | `.claude/commands/local.md` | built |
| Skills `local-*` | `.claude/skills/` | built |
| Registration in Claude Code (user scope) + Codex | `nas-agents/scripts/install.sh` | built |
| Whisper (whisper.cpp, large-v3-turbo) on the Mac, via `aimode transcribe` | `local_transcribe` | built |

The old auto-triggering `aimode` skill, the `/aimode` command and the aimode usage section in the
root `AGENTS.md` are removed.

## 3. Modes and gating (enforced by the server, not by prompts)

State is per server process (= per agent session), starts at `off`, never persisted.

| Mode | Enabled by | Tools listed | Cloud verifies local output | Mac unavailable / local fails twice |
|---|---|---|---|---|
| `off` | default, `/local off` | `local_enable` only | — | all work in the cloud |
| `prefer` | `/local` + user OK | all | yes — local output is a draft | cloud does the work, says so |
| `strict` | `/local strict` + user OK | all | no — accepted as is, reported as **unverified**; code still has to pass tests before commit | `local_submit` or stop and report; the cloud never takes over |

- `local_enable(mode)` description: only call after the user explicitly confirmed via `/local`.
- Every other tool call while `off` → error "local mode is disabled".
- `local_enable` / `local_disable` emit `notifications/tools/list_changed`.
- Clients that ignore `list_changed` (Codex is registered this way): `AIMODE_MCP_LIST_ALL=1` keeps all
  tools listed, calls are still hard-rejected while `off`.
- Mac-side failures come back as mode-specific instructions (prefer: "do it in the cloud"; strict:
  "queue or stop"). A Mac that is up with no/another model loaded is not "unavailable": the answer is
  `local_ensure_profile`.

Strict-mode rules for the orchestrator (in `/local` and `local-offload`): the cloud only plans and
dispatches with short, template-based instructions; never reads raw pages/files/logs itself; local
fails twice → stop and report.

## 4. Tools

| Tool | Behaviour |
|---|---|
| `local_enable(mode)` | `prefer` \| `strict` |
| `local_disable(shutdown?)` | back to `off`; `shutdown=true` also runs `aimode off` (agent asks the user first) |
| `local_status` | session mode + Mac availability: `ready` \| `loading` \| `aimode_off` \| `aimode_error` \| `mac_unreachable`, active profile, API health |
| `local_capabilities` | source of truth: profiles (downloaded, vision, ctx), services (`browser`, `whisper`), job worker, templates and their vars |
| `local_ensure_profile(profile)` | no-op if active; else `aimode on` / `aimode switch`, waits for health; returns load seconds. Turning AI Mode on quits the Mac's managed apps |
| `local_run(prompt \| template+vars, system?, profile?, max_tokens?, temperature?, images?, json_schema?, skill?)` | one completion on the loaded model; never switches (`profile` asserts, error on mismatch); `json_schema` constrains and validates output; images = NAS paths or data URIs |
| `local_submit(profile, prompt \| task_file \| template+vars, priority?, json_schema?, skill?)` | async job → `job_id` |
| `local_job_status` / `local_job_result` / `local_jobs` | queue inspection |
| `local_browse(instruction, url?, max_steps?, extract?, allow_submit?)` | see §7 |
| `local_transcribe(path, language?, output_dir?)` | audio/video on the NAS → .txt/.srt/.json transcripts + preview; see §7a |
| `local_feedback(id, verdict, skill?, note?)` | `accepted` \| `fixed` \| `redone` \| `unverified` → metrics |

Batching rules are part of the tool descriptions: one model at a time; plan first, group by profile,
one batch per profile, don't switch mid-task.

## 5. Mac availability

- Reachability: 2 s TCP probe of the Mac's sshd, then `aimode status --json` over SSH (15 s cap).
  An unreachable Mac answers in ~2–3 s, never hangs.
- Optional Wake-on-LAN (`AI_MAC_WOL_MAC`) before `aimode on`, in `ensure_profile` and in the worker.
- **Planned:** a "busy" flag when the Mac is in interactive use.

## 6. Async jobs

- Files under `~/.local/share/aimode-mcp/jobs/{queued,running,done}`; atomic writes.
- Worker: single instance (flock), drains the queue grouped by profile — the already-loaded profile
  first, otherwise the profile of the highest-priority/oldest job. Mac unavailable → waits with
  backoff (30 s → 10 min); one retry per job; jobs left in `running/` after a crash are requeued.
- `AIMODE_NOTIFY_CMD` receives a one-line summary on stdin per finished job (Telegram/email sender).
- Sync `local_run` stays for short interactive calls.

## 7. Browser (`local_browse`, skill `local-browser`)

- Cloud = navigator: one high-level instruction per call, judges results, handles anything risky.
- Local = eyes + hands: headless Chromium on the NAS (playwright-core; falls back to the newest
  Chromium in `~/.cache/ms-playwright`). Per step the local model gets URL, title, numbered visible
  interactive elements, trimmed visible text, plus a screenshot when the active profile has vision,
  and returns one JSON action (click/type/select/press/scroll/goto/back/wait/done/blocked/need_decision).
  Up to `max_steps` (default 8, cap 30).
- Report to the cloud, compact: `status`, url, title, ≤7-line summary, top-10 elements `[id] label`,
  extracted `data` (capped), reason, last actions.
- Escalation: `need_decision` for ambiguity, captcha, login/2FA, payment, destructive actions;
  password fields, submit buttons, login/pay/delete-like labels and Enter in non-search fields are
  intercepted unless `allow_submit=true` (only after explicit user OK). Two failures in a row → `blocked`.
- Raw HTML/DOM/screenshots stay on NAS/Mac; every action is logged to `browser.log.jsonl`.
- Session (cookies, last URL) persists between calls; omit `url` to continue.

## 7a. Speech-to-text (`local_transcribe`, skill `local-transcribe`)

- Over SSH only: no extra network service or key on the Mac; independent of AI Mode and the loaded
  profile (Whisper turbo needs ~2 GB).
- The NAS CPU is weak, so it never decodes: audio files are sent as they are; from video only the audio
  track is stream-copied out (`ffmpeg -vn -c:a copy` → .mka). The Mac converts to 16 kHz mono WAV and
  runs `whisper-cli` (Metal) — 9 s of speech in ~2 s.
- Results on the NAS (`~/.local/share/aimode-mcp/transcripts/`): `.txt` with `[hh:mm:ss]` marks, `.srt`,
  `.json`; the tool returns paths + a ≤1500-char preview.
- `aimode services --json` → `services.whisper` in `local_capabilities`; `aimode doctor` checks it.

## 8. Profiles (Mac, `models.json`)

| Profile | Model | Use for | Load (measured) |
|---|---|---|---|
| `fast` | Qwen3-4B-Instruct-2507 Q4_K_M | short Q&A, transforms, batch items | 5 s; ~76 tok/s |
| `coding` | Qwen3-Coder-30B-A3B Q4_K_M | code drafts, agentic tasks | 11 s; ~74 tok/s |
| `browser` | Qwen3.6-35B-A3B UD-Q4_K_M + mmproj (vision, MoE 3B active) | page/screenshot understanding | 17 s; ~42 tok/s, one vision browse step ~8–17 s |
| `brain` | Qwen3.5-122B-A10B UD-IQ3_S (~46.6 GB), ctx 32k, KV q8_0 | hardest reasoning, long-doc synthesis | minutes (not yet live-tested) |

Choice of `brain`: the freshest model with the most parameters that fits the 56 GB GPU wired limit
with room for context. Qwen3.5-122B-A10B at Unsloth dynamic 3-bit beats the previous brain, dense Qwen2.5-72B (removed 2026-10-18), at roughly the
same size and, with 10B active parameters, should be several times faster. Rejected:
gpt-oss-120B (~63 GB, over the limit); Mistral Small 4 119B (Q3 ≈ 50–55 GB, known Metal output bug in
llama.cpp); Qwen3.6-27B / Qwen3.8-27B (fresher but small).

Context/memory: `llama.parallel = 1` globally (llama-server splits ctx across slots); per-profile
overrides `ctx_size`, `parallel`, `cache_type_k/v`, `extra_args`.

Evaluate before replacing anything: `aimode benchmark` per profile plus 3–5 real tasks side by side;
results go to `docs/benchmarks.md`.

## 9. Mac-side requirements (done)

- `api.host` = the Mac's LAN IP; `api.api_key` set (passed to llama-server through `~/.aimode/api_key`,
  0600, not argv); the NAS reads the same key from `nas-agents/config.env`.
- `aimode` finds `llama-server` in Homebrew prefixes even with the bare PATH of a non-interactive SSH.
- `aimode models --json` for `local_capabilities`; split GGUF downloads are recognised.

## 10. Templates

`templates/<group>/<name>.md` with optional front matter (`system`, `max_tokens`, `profile`) and
`{{placeholders}}`; `{{input}}` defaults to `prompt`. Missing vars are an error. Shipped:
`fast/extract-json`, `fast/summarize`, `fast/classify`, `fast/transform`, `coding/file-draft`,
`coding/fix-from-error`, `brain/synthesize`, `browser/page-report`.

## 11. Metrics

Every `local_run`, job and browse appends to `~/.local/share/aimode-mcp/metrics.jsonl`: skill,
profile, mode, duration, model load time, input/output size, ok. `local_feedback` adds the verdict.
`aimode-mcp report [days]` summarises per skill — use it to drop or rework skills that don't pay off.

## 12. Open

- Live validation of `brain` (needs a real `aimode on brain`, which quits the managed apps).
- "Busy" flag for interactive Mac use.
- Mirror the skills for Codex if it is used beyond fallback.

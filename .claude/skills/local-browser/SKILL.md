---
name: local-browser
description: Use when a task requires browsing websites, reading pages, filling forms, scraping, or checking UI. Cloud agent navigates and decides; the local model (profile browser) does perception and low-level actions so raw HTML, DOM trees and screenshots never reach the cloud.
---

Step 0: `local_status` / mode rules as in `local-offload`. `local_capabilities` must report
`services.browser: true`.

# Roles
- **Cloud (navigator):** sets a goal per page, chooses the next high-level step, judges results,
  handles anything ambiguous or risky.
- **Local (eyes + hands):** `local_browse` — headless Chromium on the NAS; the local model reads the
  page, picks elements, acts (click/type/scroll/wait) for up to `max_steps` micro-steps (default 8),
  re-checks state, and returns a compact report.

# Loop
1. `local_ensure_profile("browser")` (vision; text-only profiles work too, without screenshots).
2. `local_browse(instruction, url?)` with ONE high-level instruction, e.g. "open X and find the pricing
   page", "fill the email field, stop before submit", "extract the plans table as JSON" (+ `extract`).
   Omit `url` to continue on the last page — the session keeps cookies and position.
3. Read the report: `status` done | blocked | need_decision, url, title, 3-7 line summary, top
   elements `[id] label`, `data`, what was tried.
4. Decide the next instruction. Repeat.

# Escalation
- `need_decision`: multiple plausible targets, unexpected page, captcha, login/2FA, payment, destructive
  or irreversible action. Ask the user when it involves their accounts, data or money.
- `blocked` after 2 failed attempts: rephrase once, then (prefer) look yourself / (strict) stop and report.
- Never request raw HTML or screenshots by default.

# Rules
- Never log in, submit personal data, or pay without explicit user OK; only then pass `allow_submit=true`.
- Every action is logged on the NAS (`~/.local/share/aimode-mcp/browser.log.jsonl`).

---
name: local-code-draft
description: Use for routine, well-specified coding work — boilerplate, DTOs, mappers, CRUD, test scaffolding, fixtures, migrations from a clear spec, mass mechanical refactors, docstrings. Local model drafts, cloud reviews.
---

Step 0: `local_status` / mode rules as in `local-offload`.

1. Write a precise spec: files, signatures, conventions, a real example from the repo.
2. `local_ensure_profile("coding")`.
3. `local_run` per file or small group with template `coding/file-draft` (full file) — ask for a unified
   diff for edits to existing files.
4. Apply the result, run linters/tests, review the diff.
5. Tests fail: one local retry with `coding/fix-from-error`, then fix in the cloud (prefer) or stop and
   report (strict).
6. `local_feedback` with the verdict.

# Never
- Offload architecture, domain-logic decisions, security or concurrency code.
- Commit local output without review and passing tests — this applies in strict mode too.

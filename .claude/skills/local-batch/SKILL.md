---
name: local-batch
description: Use for many small, similar, non-urgent operations — translating strings, renaming, tagging, classifying records, cleaning data, generating descriptions, converting formats. Local model processes the batch, cloud validates a sample.
---

Step 0: `local_status` / mode rules as in `local-offload`.

1. Define the input format, output schema and 2-3 worked examples.
2. `local_ensure_profile("fast")`; `coding` if the items are code.
3. Process items in groups (~20-50 per call) with template `fast/classify` or `fast/transform` and a
   `json_schema` — the server rejects invalid JSON. Large batches → `local_submit`, one job per group.
4. Retry invalid groups once locally; collect remaining failures.
5. Prefer: review a random ~5-10% sample plus all failures; accept, redo failures in the cloud, or rerun
   locally with a better prompt. Strict: accept, report failures, mark unverified.
6. `local_feedback` with the verdict.

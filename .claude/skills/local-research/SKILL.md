---
name: local-research
description: Use for bulk reading — summarizing or extracting from many web pages, long docs, PDFs, logs, dumps, or large codebases where the operation is simple but the input is huge.
---

Step 0: `local_status` / mode rules as in `local-offload`.

1. Define the question and the output schema (JSON or bullets) yourself.
2. Chunk the input to fit the profile's `ctx_size` from `local_capabilities` (leave ~25% for the
   prompt and output; ~3.5 chars per token). Chunk on NAS with shell tools — don't read the input yourself.
3. `local_ensure_profile("fast")` for simple extraction; `brain` for nuanced synthesis.
4. **Map:** `local_run` per chunk with template `fast/extract-json` or `fast/summarize` (+ `json_schema`
   where possible). For many chunks, or overnight, use `local_submit` instead.
5. **Reduce:** merge locally (`brain/synthesize`, or `fast` for simple merges); read only the merged result.
6. Prefer: spot-check 2-3 claims against the source. Strict: report as unverified.
7. `local_feedback` with the verdict.

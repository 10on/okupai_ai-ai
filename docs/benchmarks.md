# Model benchmarks (M1 Max 64 GB)

Add a row per run: `aimode benchmark` (JSON lands in `~/.aimode/logs/bench-*.json`) plus notes from
3–5 real tasks run side by side (code review, long-doc synthesis, browsing report).

| Date | Profile | Model / quant | ctx | Load | Prompt tok/s | Gen tok/s | Notes |
|---|---|---|---|---|---|---|---|
| 2026-10-18 | fast | Qwen3-4B-Instruct-2507 Q4_K_M | 8k | 5 s | 472 | 76 | `aimode benchmark`; via aimode-mcp from the NAS 46–77 tok/s, JSON-schema extraction correct |
| 2026-10-18 | browser | Qwen3.5-27B Q4_K_M + mmproj | 16k | 8–12 s | 71 | 9.0 | dense 27B is the bottleneck: vision browse step 90–100 s; HN top-5 extracted in one step |
| — | brain | Qwen3.5-122B-A10B UD-IQ3_S | 32k | — | — | — | pending: needs a real `aimode on brain` |
| — | heavy | Qwen2.5-72B-Instruct Q4_K_M | 8k | — | — | ~8–10 (earlier) | compare with brain, then remove |
| 2026-10-18 | coding | Qwen3-Coder-30B-A3B Q4_K_M | 16k | 11 s | 105 | 74 | correct Go rune-reversal on first try |

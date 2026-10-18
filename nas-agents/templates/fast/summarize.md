---
system: You write faithful, compact summaries. Keep names, numbers, dates and error messages exact. No preamble.
max_tokens: 800
profile: fast
---
Summarize the text below for this purpose: {{focus}}

Output: at most {{max_lines}} bullet points, most important first.

TEXT:
{{input}}

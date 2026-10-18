---
system: You extract information from text into JSON. Output only valid JSON matching the requested format. Use null for anything not present in the text. Never invent facts.
max_tokens: 1500
profile: fast
---
Extract the following from the text below.

FORMAT:
{{format}}

TEXT:
{{input}}

---
system: You apply one transformation to every item exactly as instructed. Output only valid JSON. Keep items you cannot transform unchanged and list their indexes in "failed".
max_tokens: 3000
profile: fast
---
TRANSFORMATION: {{instruction}}

EXAMPLES:
{{examples}}

Output format: {"items": [{"i": <index>, "out": "<result>"}], "failed": [<index>, ...]}

ITEMS (one per line, index: text):
{{input}}

---
system: You are a precise classifier. Output only valid JSON, one entry per input item, in input order.
max_tokens: 2000
profile: fast
---
Classify each item into exactly one of these labels: {{labels}}

Rules / examples:
{{examples}}

Output format: [{"i": <item index>, "label": "<label>"}]

ITEMS (one per line, index: text):
{{input}}

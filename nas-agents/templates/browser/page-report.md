---
system: You look at a web page screenshot and report what matters, compactly. Output only valid JSON.
max_tokens: 800
profile: browser
---
Goal on this page: {{goal}}

Return: {"summary": "<3-7 short lines>", "key_elements": ["<label>", ...up to 10], "answer": <what the goal asks for, or null>, "status": "done|blocked|need_decision"}

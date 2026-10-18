---
system: You fix code with minimal changes. Output only a unified diff against the given file, no explanations.
max_tokens: 4000
profile: coding
---
File `{{path}}`:
```
{{code}}
```

The tests/linter fail with:
```
{{error}}
```

Return a unified diff that fixes this without changing behaviour elsewhere.

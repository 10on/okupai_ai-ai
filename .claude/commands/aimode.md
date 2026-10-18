---
description: Check connectivity to the aimode LLM API on the Mac and show model profiles
---

Check the connection to the aimode API and report status to the user.

!`cd nas-agents 2>/dev/null && source scripts/connect.sh 2>&1; echo "---profiles---"; cat ../mac-ai-mode/cmd/aimode/defaults/models.json 2>/dev/null`

From the output above, tell the user:
1. Whether the API is reachable right now; if not, that `ssh ai-mac aimode on` is needed.
2. The available model profiles (fast/coding/browser/brain) with a one-line "use this for..." each.
3. That exported env vars only last for the current real shell — tool calls in separate
   invocations should re-read `nas-agents/config.env` directly instead of relying on inherited env.

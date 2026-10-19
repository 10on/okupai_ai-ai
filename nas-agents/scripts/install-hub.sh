#!/usr/bin/env bash
# Hub side (run on the NAS):    install-hub.sh serve
#   installs and starts agent-hub as a systemd --user service (port HUB_PORT, default 7800)
# Agent side (every machine):   install-hub.sh agent <name> <hub-url> <token> [note]
#   registers the hub's HTTP MCP endpoint + push hook in Claude Code and Codex. Create the token on the NAS
#   with:  node nas-agents/hub/hub.mjs add-agent <name>
set -euo pipefail
HUB="$(cd "$(dirname "${BASH_SOURCE[0]}")/../hub" && pwd)/hub.mjs"

case "${1:-}" in
serve)
    mkdir -p ~/.config/systemd/user
    sed "s|@HUB@|$HUB|" "$(dirname "$HUB")/../systemd/agent-hub.service" > ~/.config/systemd/user/agent-hub.service
    systemctl --user daemon-reload
    systemctl --user enable --now agent-hub.service
    systemctl --user --no-pager --lines=0 status agent-hub.service | head -3
    ;;
agent)
    # Pure HTTP: nothing but curl on the client (curl.exe ships with Windows 10+).
    NAME="${2:?name}"; URL="${3:?hub url, e.g. http://192.168.8.x:7800}"; TOKEN="${4:?token}"; NOTE="${5:-}"
    URL="${URL%/}"
    claude mcp remove -s user agent-hub >/dev/null 2>&1 || true
    claude mcp add -s user --transport http agent-hub "$URL/mcp" \
        -H "Authorization: Bearer $TOKEN" -H "X-Hub-Note: $NOTE"
    # Codex reads the bearer token from an env var — export HUB_TOKEN in the shell profile.
    codex mcp remove agent-hub >/dev/null 2>&1 || true
    codex mcp add agent-hub --url "$URL/mcp" --bearer-token-env-var HUB_TOKEN
    # Claude Code hooks: push new messages into a busy agent (Codex has no equivalent — it polls hub_inbox)
    SETTINGS=~/.claude/settings.json
    [[ -f "$SETTINGS" ]] || echo '{}' > "$SETTINGS"
    node -e '
      const fs=require("fs"),[f,url,token]=process.argv.slice(1),s=JSON.parse(fs.readFileSync(f,"utf8"));
      const cmd=`curl -s -m 2 -X POST --data-binary @- -H "Authorization: Bearer ${token}" ${url}/hook`;
      s.hooks??={};
      for(const ev of ["PostToolUse","UserPromptSubmit","Stop"]){
        s.hooks[ev]=(s.hooks[ev]||[]).filter(g=>!g.hooks?.some(h=>/\/hook\b|hub\.mjs hook/.test(h.command||"")));
        s.hooks[ev].push({hooks:[{type:"command",command:cmd,timeout:5}]});
      }
      fs.writeFileSync(f,JSON.stringify(s,null,2));' "$SETTINGS" "$URL" "$TOKEN"
    chmod 600 "$SETTINGS"
    echo "registered agent-hub as '$NAME' (HTTP). Codex: export HUB_TOKEN=<token>"
    ;;
*) echo "usage: install-hub.sh serve | agent <name> <hub-url> <token> [note]" >&2; exit 2 ;;
esac

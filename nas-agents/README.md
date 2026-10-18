# nas-agents

The NAS side of the setup. Claude Code and Codex run here; by default they never touch the Mac.
Local-LLM offload is switched on per session with `/local` and goes through one MCP server,
`aimode-mcp`, which enforces the gating itself.

```
nas-agents/
├── aimode-mcp/        Go source of the MCP server + job worker (no dependencies)
├── bin/aimode-mcp     linux/amd64 build (gitignored)
├── browser/agent.mjs  headless browser agent for local_browse (playwright-core)
├── templates/         prompt templates per profile, filled by the cloud agent
├── systemd/           aimode-worker.service template
├── scripts/install.sh registers everything in Claude Code + Codex
├── scripts/connect.sh exports OPENAI_BASE_URL/KEY for plain OpenAI-compatible tools
└── config.env         local settings (gitignored) — copy config.example.env
```

Spec: [`../docs/local-offload-spec.md`](../docs/local-offload-spec.md).

## Install / update

```bash
# build (anywhere with Go 1.27)
cd nas-agents/aimode-mcp && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../bin/aimode-mcp .

# on the NAS
cd nas-agents
cp config.example.env config.env   # AI_MAC_HOST, AI_MAC_USER, AI_MAC_API_KEY (= Mac's api.api_key), AI_MAC_AIMODE
scripts/install.sh
```

Updating just the binary while the worker runs: copy to `bin/aimode-mcp.new`, then
`mv -f bin/aimode-mcp.new bin/aimode-mcp && systemctl --user restart aimode-worker`
(writing over a running executable fails with "text file busy").

`install.sh` is idempotent: `Host ai-mac` in `~/.ssh/config`, `npm install` for the browser agent,
`claude mcp add -s user aimode`, `codex mcp add aimode` (with `AIMODE_MCP_LIST_ALL=1`), links `/local`
and the `local-*` skills into `~/.claude` (and `/local` into `~/.codex/prompts`), and starts the
`aimode-worker` systemd user service.

## Using it

In an agent session:

| Command | Mode | Mac unavailable → |
|---|---|---|
| `/local off` (default) | off | all work in the cloud; only `local_enable` is visible |
| `/local` | prefer | the cloud does the work itself |
| `/local strict` | strict | no cloud fallback: queue (`local_submit`) or stop |

Tools also include `local_transcribe` (Whisper on the Mac for any audio/video on the NAS) and
`local_browse`. The skills (`local-offload`, `local-research`, `local-batch`, `local-code-draft`, `local-browser`,
`local-transcribe`) are always visible to the agent but check the mode first and never enable it.

Useful by hand:

```bash
bin/aimode-mcp report 7                         # what got offloaded, verdicts per skill
systemctl --user status aimode-worker           # async job worker
journalctl --user -u aimode-worker -f
ls ~/.local/share/aimode-mcp/jobs/{queued,running,done}
```

## Plain OpenAI-compatible tools

`source scripts/connect.sh` still exports `OPENAI_BASE_URL` / `OPENAI_API_KEY` and checks `/health`,
for tools that aren't MCP clients. AI Mode has to be on (`ssh ai-mac aimode on [profile]`).

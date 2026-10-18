# Remote access

## SSH control (primary)

AI Mode is controlled over SSH from the second Mac (M4 Pro) or a phone on the LAN.

### Enable Remote Login on the AI Mac

System Settings → General → Sharing → **Remote Login** (restrict to your user).
If `systemsetup -setremotelogin on` reports a Full Disk Access error, use the GUI,
or `sudo launchctl load -w /System/Library/LaunchDaemons/ssh.plist`.

### Key-based login

On the AI Mac the public key lives in `~/.ssh/authorized_keys`. From the client:

```bash
ssh-copy-id you@<AI_MAC_IP>     # once
```

`~/.ssh/config` on the client:

```
Host ai-mac
    HostName <AI_MAC_IP>
    User you
    IdentityFile ~/.ssh/id_rsa
    ServerAliveInterval 30
    ServerAliveCountMax 3
```

Then:

```bash
ssh ai-mac aimode on
ssh ai-mac aimode status
ssh ai-mac aimode switch coding
ssh ai-mac aimode off
```

> `aimode on` over SSH does **not** prompt — it proceeds on the `services.json` allowlist.
> Run `ssh ai-mac aimode on --dry-run` first to preview.

### Reliability notes

- aimode verifies SSH (port 22) is reachable **before** stopping anything, and refuses to
  proceed over a remote session if it is not (ТЗ §9).
- `sshd` is on the hard never-touch list, so control is never stopped by AI Mode.
- Reserve the Ethernet adapter's IP in your router's DHCP so `ai-mac` stays stable.

## Inference API (LAN)

With AI Mode on, the OpenAI-compatible API is at `http://<api.host>:<api.port>`:

```bash
curl http://<AI_MAC_IP>:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <api.api_key>' \
  -d '{"model":"aimode","messages":[{"role":"user","content":"hi"}]}'
```

`/health` is unauthenticated; everything under `/v1` needs the key. Point CLI agents, editors and
scripts at this base URL. NAS agents reach it through `nas-agents/` (aimode-mcp). **Do not expose it to the
internet** — keep it on the LAN or behind a VPN/tunnel.

## Management HTTP API

Optional and not enabled by default (ТЗ §3C). If added later it must authenticate, bind to
the trusted network only, and never allow arbitrary shell execution. SSH covers control for now.

## Wake-on-LAN

`womp` is enabled on this machine. From the client: `wakeonlan <MAC>` (or `caffeinate`-kept-awake
while in AI Mode). Closed-lid behavior is covered in `recovery.md`.

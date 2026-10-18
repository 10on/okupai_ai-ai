# Environment audit — Stage 1

Snapshot of the AI Mac taken before any changes (2026-10-18). Nothing was disabled during the audit.

## Machine

- MacBook Pro M1 Max (MacBookPro18,4), 10 cores (8P/2E), **64 GB**.
- macOS 15.7.7 (24G720). Uptime at audit: 35 days.
- `iogpu.wired_limit_mb = 0` (default; GPU gets ~48 GB of 64). Raise for 70B-class models.
- Disk: 1.8 TB, **382 GB free** after cleanup (was 246 GB).

## Network

- Ethernet 2.5G `en15` = **192.168.8.122**, Wi-Fi `en0` = 192.168.8.201 (same subnet).
- Many VPNs present (Cloudflare WARP, GlobalProtect, ZeroTier, OpenVPN, Tunnelblick,
  TunnelBear, WireGuard, V2Box) — any can hijack routing; consider while in AI Mode.
- Firewall: **off**.
- `ComputerName = "Ноутбук — Denis"`, `LocalHostName = Noutbuk-Denis`.

## SSH

- Was **off** at audit; enabled during setup. Key login via `~/.ssh/authorized_keys` works.

## Power (ТЗ §5)

- `sleep 1`, `womp 1` (wake-on-net), `powernap 1`, `networkoversleep 0`, `hibernatemode 3`.
- At audit, sleep was held off by DaVinci Resolve (a 673h background assertion).

## Memory consumers (candidates to stop)

Firefox (~25 procs, 12–14 GB), Cloudflare WARP (~1.7 GB), Docker (10 containers incl.
Home Assistant + its VM), iTerm2, Cursor, Spotify, Telegram, DaVinci Resolve, AnycubicSlicer.

## Autostart noise

Microsoft/Teams/OneDrive updaters, Zoom, TeamViewer, AnyDesk, Steam, Google Updater,
XQuartz, Splashtop, and more. Not touched by default.

## LLM tooling (before cleanup)

- **Ollama** (brew, not running): llama3.3:70b (+dup tag), gemma3:27b, mistral-small:22b — 72 GB.
- **LM Studio** + `lms`: Llama-3-70b-Uncensored, Qwen3-Coder-30B MLX, Llama-3.1-8B, Dolphin3-8B — 65 GB.
- **ComfyUI** — 32 GB (FLUX.1-dev etc.) — image gen, **kept**.
- mlx (py3.14), aider/codex/continue CLIs, old py3.10 + torch 2.3.
- `llama.cpp` installed during Stage 2.

## Cleanup performed

All downloaded **LLM** models (Ollama + LM Studio) deleted at the user's request (~136 GB freed).
ComfyUI / FLUX image-gen models kept. Ollama/LM Studio apps and their settings kept.

## Stage 2 validation

llama.cpp 0.7.0 + Metal: Qwen3-4B Q4_K_M at **76 tok/s** generation, 300+ tok/s prompt.
OpenAI-compatible API verified.

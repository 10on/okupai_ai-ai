# mac-ai-mode

A controlled, reversible **AI Mode** for a MacBook Pro M1 Max (64 GB) under macOS.
One command frees memory from managed apps and starts a local llama.cpp server with
an OpenAI-compatible API on the LAN; another command restores the machine — without a reboot.

Target machine: MacBook Pro M1 Max, 64 GB, macOS 15.7. Remote client: a second Mac over SSH.

## Design priorities (in order)

1. **Stability & keeping remote access** — SSH is verified before anything is stopped; `sshd`,
   WindowServer, power and audio are on a hard never-touch list.
2. **Maximum memory for the LLM** — managed apps are quit gracefully; optional GPU wired-limit bump.
3. **Reversibility** — on-disk state (atomic writes) lets `off` undo exactly what `on` did, even
   after a crash or reboot. Failed starts auto-roll-back.
4. **Ease of control** — single static Go binary, no runtime dependencies.
5. **Inference performance** — all layers on Metal (`-ngl 99`), flash-attention, MoE models.

## Install

```bash
brew install llama.cpp go      # runtime + build toolchain
cd mac-ai-mode
go build -o bin/aimode ./cmd/aimode
sudo cp bin/aimode /usr/local/bin/aimode     # or add bin/ to PATH
```

First run creates editable config under `~/.aimode/config/`.

## Usage

```bash
aimode on [model]     # free resources, start the LLM (default model if omitted)
aimode off            # stop the LLM, restore managed settings
aimode status         # mode, model, memory, pressure, swap, API health
aimode doctor         # diagnose deps, network, power, GPU, disk
aimode models         # list profiles + download state (--json for agents)
aimode switch <model> # swap the active model without leaving AI Mode
aimode logs [N]       # tail logs
aimode benchmark      # prompt/gen tokens-per-second -> JSON
aimode on --dry-run   # show the plan, change nothing
```

Remote:

```bash
ssh ai-mac aimode on
ssh ai-mac aimode status
ssh ai-mac aimode switch coding
```

## Model profiles (`~/.aimode/config/models.json`)

| Profile | Model | Notes |
|---|---|---|
| `fast`    | Qwen3-4B-Instruct Q4_K_M             | instant replies, ~2.5 GB |
| `coding`  | Qwen3-Coder-30B-A3B-Instruct Q4_K_M  | MoE, fast, strong at code |
| `browser` | Qwen3.5-27B (vision) + mmproj        | sees page screenshots, for browser/GUI agents, ~17.7 GB |
| `brain`   | Qwen3.5-122B-A10B UD-IQ3_S           | MoE, ~46.6 GB, ctx 32k, KV q8_0 — top quality that fits; needs the GPU wired-limit raised |
| `heavy`   | Qwen2.5-72B-Instruct Q4_K_M          | the previous brain, dense ~47 GB, ~8-10 tok/s — to be removed once `brain` is validated |

Per-profile keys override the global `llama` settings: `ctx_size`, `parallel`, `cache_type_k`/`cache_type_v`
(quantized KV cache), `extra_args`. `aimode models --json` lists profiles for agents.

Models download on first use via llama.cpp's `-hf` into the Hugging Face cache.

## Configuration

- `config.json` — API host/port/key, llama.cpp params, power options, restart policy.
- `models.json` — the profiles above.
- `services.json` — apps to quit on `on` (the pre-approved policy), optional launchd agents,
  and the `never` safety net.

## Safety

- `on` in an interactive terminal asks for confirmation before quitting apps; over SSH or with
  `--yes` it proceeds on the allowlist policy.
- Apps are quit **gracefully** — one with unsaved data shows its own save dialog and is left alone.
- `off` does not reopen GUI apps by design.
- See `docs/recovery.md` if something goes wrong.

## Layout

```
mac-ai-mode/
├── bin/aimode              # built binary
├── cmd/aimode/             # Go sources (package main) + embedded default configs
├── config/ ~/.aimode/      # live config created on first run
├── launchd/                # launchd templates
└── docs/                   # installation, remote-access, recovery
```

# Installation

## Requirements

- macOS on Apple silicon (validated on M1 Max, 64 GB, macOS 15.7.7).
- [Homebrew](https://brew.sh).

## 1. Dependencies

```bash
brew install llama.cpp go
```

`llama.cpp` ships with Metal support on Apple silicon — no extra flags needed.
Verify: `llama-server --version`.

## 2. Build

```bash
cd mac-ai-mode
go build -o bin/aimode ./cmd/aimode
```

The binary is static and self-contained (default configs are embedded). Install it on PATH:

```bash
sudo cp bin/aimode /usr/local/bin/aimode
```

## 3. First run

```bash
aimode doctor
```

This creates `~/.aimode/config/{config,models,services}.json` and checks dependencies,
network, power settings, GPU limit and disk.

## 4. Configure

Edit `~/.aimode/config/config.json`:

- `api.host` — bind address for the inference API. Use the LAN/Ethernet IP so other
  devices can reach it (e.g. `<AI_MAC_IP>`). Use `127.0.0.1` to keep it local-only.
- `api.port` — default `8080`.
- `api.api_key` — set a token to require `Authorization: Bearer <token>`. Required once the API is on
  the LAN (`api.host` = LAN IP). aimode hands it to llama-server via `~/.aimode/api_key` (0600), never argv.
- `llama.ctx_size`, `llama.parallel` (keep 1: llama-server splits the context across slots),
  `llama.n_gpu_layers`, `llama.flash_attn`.
- `power.caffeinate` — keep the machine awake while AI Mode is on.
- `power.wired_limit_mb` — see below.

Edit `~/.aimode/config/services.json` to curate which apps are quit on `on`.

## 5. GPU wired-memory limit (for `brain` / `heavy` only)

By default macOS lets the GPU use ~48 GB of the 64 GB. A 70B-class model needs more.
To raise it, set `power.wired_limit_mb` (e.g. `57344` = 56 GB) and allow passwordless sysctl:

```bash
sudo visudo -f /etc/sudoers.d/aimode
# add:
your_user ALL=(root) NOPASSWD: /usr/sbin/sysctl iogpu.wired_limit_mb=*
```

`aimode on` then raises the limit and `off` restores it. The limit resets on reboot anyway.
If sudo isn't authorized, aimode logs a warning and continues with the default limit.

## 6. Run at login (optional)

See `../launchd/` for a template to keep the inference server under launchd with
crash-restart. The lightweight control path (`aimode` over SSH) needs no daemon.

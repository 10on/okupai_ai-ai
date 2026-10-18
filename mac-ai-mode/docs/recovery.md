# Recovery

AI Mode is designed to fail safe. This page covers how to get back to NORMAL by hand if needed.

## State and logs

- State: `~/.aimode/state.json` (current mode, model, PIDs, what was changed).
- Logs: `~/.aimode/logs/aimode.log` (control) and `~/.aimode/logs/server.log` (llama.cpp).
- Lock: `~/.aimode/aimode.lock` (flock; released automatically on exit).

```bash
aimode status
aimode logs 80
```

## Normal recovery

```bash
aimode off          # stops the server, kills caffeinate, restores GPU limit & launchd agents
```

`off` is idempotent — safe to run repeatedly. It does **not** reopen GUI apps by design;
reopen what you need manually (e.g. `open -a Firefox`).

## If `on` failed partway

A failed `on` auto-rolls-back: the server is stopped, caffeinate released, the GPU limit and any
disabled launchd agents restored, and the mode set back to NORMAL. Check `aimode logs` for the reason.

## Manual cleanup (if aimode itself is unavailable)

```bash
# stop the inference server
pkill -f llama-server

# release our sleep guard
pkill -f 'caffeinate -i -m -s'

# restore GPU wired limit to default (if it was raised)
sudo sysctl iogpu.wired_limit_mb=0

# re-enable a launchd agent that was disabled
launchctl enable gui/$(id -u)/<label>

# reset aimode state
echo '{"mode":"NORMAL"}' > ~/.aimode/state.json
```

## A stale lock

If aimode reports "another operation is in progress" but nothing is running:

```bash
rm -f ~/.aimode/aimode.lock
```

(The lock is advisory; removing it is safe when no aimode process is active.)

## Headless / closed-lid operation (ТЗ §5 — verify experimentally)

`caffeinate` prevents idle sleep but does **not** guarantee operation with the lid closed.
For sustained clamshell runs on this machine, confirm each of these yourself:

- External power connected and an external display attached (clamshell needs both historically).
- `pmset -g` shows `sleep` prevented while AI Mode is on.
- A model keeps responding over the LAN with the lid shut for your target duration.
- Ethernet (USB adapter) stays up after display sleep; `womp` allows wake-on-network.

If the machine still sleeps with the lid closed, prefer lid-open in the dock, or investigate
`pmset -a disablesleep 1` (advanced, test carefully — it is reversible with `disablesleep 0`).

## SSH safety

aimode never stops `sshd` and verifies port 22 before touching services. If you are ever locked
out, you still have local keyboard access: run `aimode off` in Terminal.

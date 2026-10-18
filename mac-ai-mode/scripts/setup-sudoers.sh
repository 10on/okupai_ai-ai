#!/bin/bash
# Grant passwordless sysctl for the GPU wired-memory limit ONLY, so `aimode`
# can raise/lower iogpu.wired_limit_mb for large models without a password.
# Run with: sudo bash scripts/setup-sudoers.sh
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must run as root. Use: sudo bash $0" >&2
  exit 1
fi

# The real (non-root) login user, even though we're running under sudo.
# Allow an explicit override: sudo AIMODE_USER=<you> bash scripts/setup-sudoers.sh
USER_NAME="${AIMODE_USER:-${SUDO_USER:-$(logname 2>/dev/null || true)}}"
if [ -z "$USER_NAME" ] || [ "$USER_NAME" = "root" ]; then
  echo "Could not determine your login user. Re-run as: sudo AIMODE_USER=<you> bash $0" >&2
  exit 1
fi

SYSCTL=/usr/sbin/sysctl
DEST=/etc/sudoers.d/aimode
RULE="$USER_NAME ALL=(root) NOPASSWD: $SYSCTL iogpu.wired_limit_mb=*"

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
printf '%s\n' "$RULE" > "$TMP"

# Validate syntax BEFORE touching the real sudoers tree.
if ! visudo -cf "$TMP" >/dev/null; then
  echo "Syntax validation failed; nothing changed." >&2
  exit 1
fi

install -m 440 -o root -g wheel "$TMP" "$DEST"
echo "Installed $DEST"
echo "  rule: $RULE"

# Verify the rule actually grants passwordless access for the user.
if sudo -u "$USER_NAME" sudo -n "$SYSCTL" iogpu.wired_limit_mb=0 >/dev/null 2>&1; then
  echo "Verified: passwordless 'sysctl iogpu.wired_limit_mb' works for $USER_NAME."
  echo "(limit left at 0 = system default; aimode will set it when you run 'aimode on brain')"
else
  echo "WARNING: the passwordless test did not pass. Check $DEST." >&2
  exit 1
fi

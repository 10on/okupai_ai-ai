#!/usr/bin/env bash
# Source this in a NAS console session before running any CLI agent:
#   source scripts/connect.sh
# It points OpenAI-compatible tooling at the aimode API on the Mac and
# confirms the server is actually up before you start working.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="$SCRIPT_DIR/../config.env"

if [[ ! -f "$ENV_FILE" ]]; then
    echo "Missing $ENV_FILE — copy config.example.env to config.env and fill it in." >&2
    return 1 2>/dev/null || exit 1
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

if [[ -z "${AI_MAC_HOST:-}" ]]; then
    echo "AI_MAC_HOST is not set in $ENV_FILE" >&2
    return 1 2>/dev/null || exit 1
fi

export OPENAI_BASE_URL="http://${AI_MAC_HOST}:${AI_MAC_PORT:-8080}/v1"
export OPENAI_API_KEY="${AI_MAC_API_KEY:-none}"

if curl -fsS --max-time 3 "http://${AI_MAC_HOST}:${AI_MAC_PORT:-8080}/health" >/dev/null 2>&1; then
    echo "aimode API reachable at $OPENAI_BASE_URL"
else
    echo "WARNING: aimode API not reachable at http://${AI_MAC_HOST}:${AI_MAC_PORT:-8080} — is AI Mode on? (ssh ai-mac aimode on)" >&2
fi

#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "larkdesk is macOS-only (reuses local Lark.app / 飞书 session)" >&2
  exit 1
fi

if ! command -v go >/dev/null; then
  echo "Go 1.24+ required: https://go.dev/dl/" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

BIN="${HOME}/.local/bin"
mkdir -p "$BIN"
go build -ldflags="-s -w" -o "$BIN/larkdesk" ./cmd/larkdesk

if ! command -v larkdesk >/dev/null; then
  echo "installed, but larkdesk is not on PATH. add ~/.local/bin:" >&2
  echo '  export PATH="$HOME/.local/bin:$PATH"' >&2
fi

echo "ok: $(command -v larkdesk || echo "$BIN/larkdesk")"
echo "open 飞书 and log in, then: larkdesk whoami"

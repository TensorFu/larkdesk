#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "larkdesk is macOS-only (reuses local Lark.app / 飞书 session)" >&2
  exit 1
fi

if ! command -v python3 >/dev/null; then
  echo "python3 >= 3.10 required" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

if command -v pipx >/dev/null; then
  pipx install --force "$ROOT"
elif command -v uv >/dev/null; then
  uv tool install --force "$ROOT"
else
  python3 -m pip install --user --upgrade pip
  python3 -m pip install --user --force-reinstall "$ROOT"
fi

if ! command -v larkdesk >/dev/null; then
  echo "installed, but larkdesk is not on PATH. add ~/.local/bin:" >&2
  echo '  export PATH="$HOME/.local/bin:$PATH"' >&2
fi

echo "ok: $(command -v larkdesk || echo ~/.local/bin/larkdesk)"
echo "open 飞书 and log in, then: larkdesk whoami"

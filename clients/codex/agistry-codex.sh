#!/usr/bin/env bash
# Entry point for Codex hooks and the host bridge. Share the existing client config.
set -eu
if [ -f "$HOME/.config/agistry/client.env" ]; then
  set -a
  . "$HOME/.config/agistry/client.env"
  set +a
fi
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$HERE/agistry_codex.py" "$@"

#!/usr/bin/env bash
# Install the Codex hooks, queued bridge, and adapted Agistry skill.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$HERE/install.py" "$@"

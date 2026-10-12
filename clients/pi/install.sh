#!/usr/bin/env bash
# Installs the agistry pi client: the extension that registers each pi session and
# delivers agistry mail into it, plus the shared pieces (config, agistry skill,
# in-process client library, device providers). Safe to re-run.
#
# Usage: ./install.sh [--url URL] [--token TOKEN]     (honors PI_CODING_AGENT_DIR)
#        ./install.sh --uninstall
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXT="${PI_CODING_AGENT_DIR:-$HOME/.pi/agent}/extensions/agistry.ts"

case "${1:-}" in
  --uninstall)
    rm -f "$EXT"; echo "  removed $EXT"
    echo "  left the shared skill, library, config and providers (other agents may use them)"
    exit 0 ;;
  -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
esac

command -v node >/dev/null 2>&1 || echo "  WARNING: node not found; pi needs it" >&2

echo "Installing agistry pi client"
"$HERE/../shared/install.sh" "$@"
mkdir -p "$(dirname "$EXT")"
install -m 0644 "$HERE/agistry.ts" "$EXT"
echo "  extension -> $EXT"
echo
echo "Done. Start pi (or run /reload in a running one); the session appears in the dashboard."

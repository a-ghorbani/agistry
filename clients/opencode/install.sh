#!/usr/bin/env bash
# Installs the agistry opencode client: the plugin that registers each opencode session
# and delivers agistry mail into it, plus the shared pieces (config, agistry skill,
# in-process client library, device providers). Safe to re-run.
#
# Usage: ./install.sh [--url URL] [--token TOKEN]     (honors OPENCODE_CONFIG_DIR)
#        ./install.sh --uninstall
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN="${OPENCODE_CONFIG_DIR:-$HOME/.config/opencode}/plugins/agistry.js"

case "${1:-}" in
  --uninstall)
    rm -f "$PLUGIN"; echo "  removed $PLUGIN"
    echo "  left the shared skill, library, config and providers (other agents may use them)"
    exit 0 ;;
  -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
esac

echo "Installing agistry opencode client"
"$HERE/../shared/install.sh" "$@"
mkdir -p "$(dirname "$PLUGIN")"
install -m 0644 "$HERE/agistry.js" "$PLUGIN"
echo "  plugin -> $PLUGIN"
echo
echo "Done. Restart opencode; each session appears in the dashboard once it starts."

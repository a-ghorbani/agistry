#!/usr/bin/env bash
# Installs the agistry resource providers ONCE PER HOST into ~/.config/agistry/providers,
# next to the shared client.env. Providers belong to the host (the devices stay plugged
# in whichever agent or profile is running), so every client uses this one copy: the
# Claude Code and Codex installers call this script, their session hooks start the
# launcher from here, and the systemd unit runs from here. Safe to re-run.
#
# Installing starts nothing: providers only run once AGISTRY_PROVIDERS names them (see
# README.md). --uninstall stops any running providers and removes this copy; removing
# a single Claude profile or the Codex client never does.
#
# Usage: ./install.sh [--uninstall]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST="$HOME/.config/agistry/providers"

case "${1:-}" in
  "")
    mkdir -p "$DEST"
    for f in "$HERE"/agistry-*.sh; do
      install -m 0755 "$f" "$DEST/$(basename "$f")"
    done
    install -m 0644 "$HERE/agistry-provider@.service" "$DEST/agistry-provider@.service"
    echo "  providers -> $DEST (none run until AGISTRY_PROVIDERS is set)"
    ;;
  --uninstall)
    [ -x "$DEST/agistry-providers.sh" ] && "$DEST/agistry-providers.sh" stop || true
    rm -rf "$DEST"
    echo "  removed $DEST"
    echo "  if you enabled the systemd unit: systemctl --user disable --now 'agistry-provider@<name>'"
    ;;
  -h|--help) sed -n '2,12p' "$0" ;;
  *) echo "unknown arg: $1" >&2; exit 1 ;;
esac

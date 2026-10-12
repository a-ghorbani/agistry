#!/usr/bin/env bash
# Show this host's agistry resource providers. They run as user services (launchd on
# macOS, systemd on Linux) that install.sh sets up for each name in AGISTRY_PROVIDERS
# (~/.config/agistry/client.env); this script only reports on them.
#
#   agistry-providers.sh list      # available providers, and which are enabled
#   agistry-providers.sh status    # whether each enabled one is running
#
# To change which run: edit AGISTRY_PROVIDERS, then re-run clients/providers/install.sh.
set -u

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
enabled() { printf '%s' "${AGISTRY_PROVIDERS:-}" | tr ',' ' '; }

status_one() {
  case "$(uname -s)" in
    Darwin)
      local pid
      pid="$(launchctl print "gui/$(id -u)/dev.agistry.provider.$1" 2>/dev/null | sed -n 's/^[[:space:]]*pid = //p')"
      if [ -n "$pid" ]; then echo "$1: running (launchd, pid $pid)"
      elif launchctl print "gui/$(id -u)/dev.agistry.provider.$1" >/dev/null 2>&1; then
        echo "$1: loaded but not running — see ~/Library/Logs/agistry/provider-$1.log"
      else echo "$1: not installed — re-run clients/providers/install.sh"; fi ;;
    Linux)
      echo "$1: $(systemctl --user is-active "agistry-provider@$1" 2>/dev/null || true) (systemd agistry-provider@$1)" ;;
    *) echo "$1: no service manager on this OS" ;;
  esac
}

case "${1:-status}" in
  list)
    echo "available:"
    for f in "$DIR"/agistry-*-provider.sh; do
      [ -e "$f" ] || continue
      n="${f##*/agistry-}"; echo "  ${n%-provider.sh}"
    done
    echo "enabled (AGISTRY_PROVIDERS): ${AGISTRY_PROVIDERS:-<none>}" ;;
  status)
    set -- $(enabled)
    [ $# -eq 0 ] && { echo "AGISTRY_PROVIDERS unset — no providers enabled"; exit 0; }
    for n in "$@"; do status_one "$n"; done ;;
  *) sed -n '2,9p' "$0" ;;
esac

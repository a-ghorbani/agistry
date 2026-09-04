#!/usr/bin/env bash
# Launcher for agistry resource providers.
#
# Nothing runs unless this host is explicitly configured to run it. Set in
# ~/.config/agistry/client.env:
#
#   AGISTRY_PROVIDERS="adb"          # space/comma separated; unset or empty = none
#   AGISTRY_PROVIDER_INTERVAL=300    # re-announce cadence, seconds
#
# Most hosts have no devices attached and should run no provider at all, so the
# default is none and this script exits immediately when unconfigured. One process
# per provider per host: a pidfile guard makes repeat invocations idempotent, so
# every Claude session can call `start` without stacking daemons.
#
#   agistry-providers.sh start|stop|status|list
set -u

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INTERVAL="${AGISTRY_PROVIDER_INTERVAL:-300}"
RUNDIR="${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}"

# space/comma separated -> word list
enabled() { printf '%s' "${AGISTRY_PROVIDERS:-}" | tr ',' ' '; }

pidfile() { printf '%s/agistry-provider-%s.pid' "$RUNDIR" "$1"; }
script_for() { printf '%s/agistry-%s-provider.sh' "$DIR" "$1"; }

alive() {
  local p; p="$(cat "$(pidfile "$1")" 2>/dev/null)" || return 1
  [ -n "$p" ] && kill -0 "$p" 2>/dev/null
}

# A provider belongs to the HOST, not to whoever started it: the devices stay plugged
# in after a Claude session ends. setsid puts it in its own session and process group so
# it outlives the caller's terminal, and gives stop_one a group to kill -- otherwise the
# `sleep` between announces would be orphaned rather than stopped.
start_one() {
  local n="$1" s; s="$(script_for "$n")"
  if [ ! -x "$s" ]; then echo "no such provider: $n (looked for $s)" >&2; return 1; fi
  if alive "$n"; then echo "$n: already running (pid $(cat "$(pidfile "$n")"))"; return 0; fi
  local pid
  if command -v setsid >/dev/null 2>&1; then
    setsid "$s" --loop "$INTERVAL" >/dev/null 2>&1 < /dev/null &
    pid=$!
  else
    nohup "$s" --loop "$INTERVAL" >/dev/null 2>&1 < /dev/null &   # fallback: SIGHUP-proof only
    pid=$!
  fi
  echo "$pid" > "$(pidfile "$n")"
  echo "$n: started (pid $pid)"
}

stop_one() {
  local n="$1" p; p="$(cat "$(pidfile "$n")" 2>/dev/null)"
  if [ -n "${p:-}" ] && kill -0 "$p" 2>/dev/null; then
    # kill the whole process group when we own one (negative pid), so the in-flight
    # sleep goes too; fall back to the bare pid if it is not a group leader.
    kill -TERM -- "-$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null
    echo "$n: stopped (pid $p)"
  else
    echo "$n: not running"
  fi
  rm -f "$(pidfile "$n")"
}

case "${1:-start}" in
  start)
    set -- $(enabled)
    [ $# -eq 0 ] && { echo "AGISTRY_PROVIDERS unset — no providers to start"; exit 0; }
    for n in "$@"; do start_one "$n"; done ;;
  stop)
    set -- $(enabled)
    for n in "$@"; do stop_one "$n"; done
    [ $# -eq 0 ] && echo "AGISTRY_PROVIDERS unset — nothing to stop" ;;
  status)
    set -- $(enabled)
    [ $# -eq 0 ] && { echo "AGISTRY_PROVIDERS unset — no providers configured"; exit 0; }
    for n in "$@"; do
      if alive "$n"; then echo "$n: running (pid $(cat "$(pidfile "$n")"))"; else echo "$n: stopped"; fi
    done ;;
  list)
    echo "available:"
    for f in "$DIR"/agistry-*-provider.sh; do
      [ -e "$f" ] || continue
      n="${f##*/agistry-}"; echo "  ${n%-provider.sh}"
    done
    echo "enabled (AGISTRY_PROVIDERS): ${AGISTRY_PROVIDERS:-<none>}" ;;
  *) sed -n '2,17p' "$0" ;;
esac

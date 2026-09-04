#!/usr/bin/env bash
# Announce the Android devices attached to THIS host to agistry, and keep announcing
# them so they age out when unplugged.
#
# This is the reference "provider": agistry never discovers resources itself, because a
# phone is attached to a host, not to the registry. Whatever can see the resource is
# what registers it. Copy this shape for GPUs, serial rigs, licence dongles.
#
#   agistry-adb-provider.sh              # one pass
#   agistry-adb-provider.sh --loop [sec] # keep refreshing (default 300s)
#
# Devices are keyed by adb serial, prefixed "adb:", because that is what the tooling
# already uses. The friendly model name rides along as metadata.
set -uo pipefail

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"
HOST="$(hostname 2>/dev/null || echo unknown)"

# A full e2e suite across every feature can run for hours, so phones default to a
# generous cap. It is a backstop against an agent that is alive but has wandered off,
# not a normal expiry -- the usual release is the holder finishing, or its session dying.
MAX_HOLD="${AGISTRY_ADB_MAX_HOLD:-21600}"   # 6h
INTERVAL="${2:-300}"

command -v adb >/dev/null 2>&1 || { echo "adb not found in PATH" >&2; exit 1; }

announce_once() {
  local serial state model device n=0
  # `adb devices -l` prints "SERIAL   device  product:x model:y device:z transport_id:n"
  while read -r serial state rest; do
    [ -z "${serial:-}" ] && continue
    case "$serial" in List|'*'*) continue ;; esac
    [ "$state" = "device" ] || continue     # skip offline/unauthorized/recovery
    model="$(printf '%s' "${rest:-}" | tr ' ' '\n' | sed -n 's/^model://p')"
    device="$(printf '%s' "${rest:-}" | tr ' ' '\n' | sed -n 's/^device://p')"
    body="$(jq -nc --arg id "adb:$serial" --arg h "$HOST" --arg n "${model:-$serial}" \
                   --arg s "$serial" --arg d "${device:-}" --argjson mh "$MAX_HOLD" \
      '{id:$id, kind:"android-device", name:$n, host:$h, max_hold_seconds:$mh,
        meta:{serial:$s, model:$n, device:$d}}')"
    curl -sf --max-time 5 -H "X-Registry-Token: $TOK" "$URL/resources/register" \
         -d "$body" >/dev/null 2>&1 && n=$((n+1))
  done < <(adb devices -l 2>/dev/null)
  echo "announced $n device(s) to $URL"
}

command -v jq >/dev/null 2>&1 || { echo "jq required" >&2; exit 1; }

if [ "${1:-}" = "--loop" ]; then
  while :; do announce_once; sleep "$INTERVAL"; done
else
  announce_once
fi

#!/usr/bin/env bash
# Announce the iPhones/iPads attached to THIS host to agistry, and keep announcing
# them so they age out when unplugged. Same shape as agistry-adb-provider.sh.
#
#   agistry-ios-provider.sh              # one pass
#   agistry-ios-provider.sh --loop [sec] # keep refreshing (default 300s)
#
# Devices are keyed by UDID, prefixed "ios:", because that is what the tooling already
# takes (xcodebuild -destination id=<UDID>, devicectl --device, idevice* -u). The model
# and device name ride along as metadata.
#
# Discovery, in order:
#   macOS  `xcrun devicectl list devices` (Xcode 15+). Only paired devices on a wired
#          connection by default: a phone paired to several Macs is visible to all of
#          them over the network, and two hosts must not announce the same device.
#          AGISTRY_IOS_TRANSPORTS="wired localNetwork" opts in to network-attached ones.
#   Linux  libimobiledevice: `idevice_id -l` (USB only) + `ideviceinfo`.
# AGISTRY_IOS_TOOL=devicectl|libimobiledevice forces one.
set -uo pipefail

[ -f "$HOME/.config/agistry/client.env" ] && . "$HOME/.config/agistry/client.env"
URL="${AGISTRY_URL:-http://127.0.0.1:7070}"
TOK="${AGISTRY_TOKEN:-}"
HOST="$(hostname 2>/dev/null || echo unknown)"

# Same reasoning as Android: a full e2e suite can run for hours, so the cap is generous.
MAX_HOLD="${AGISTRY_IOS_MAX_HOLD:-21600}"   # 6h
INTERVAL="${2:-${AGISTRY_PROVIDER_INTERVAL:-300}}"
TRANSPORTS="${AGISTRY_IOS_TRANSPORTS:-wired}"

TOOL="${AGISTRY_IOS_TOOL:-}"
if [ -z "$TOOL" ]; then
  if xcrun --find devicectl >/dev/null 2>&1; then TOOL=devicectl
  elif command -v idevice_id >/dev/null 2>&1; then TOOL=libimobiledevice
  fi
fi
[ -n "$TOOL" ] || { echo "no iOS tooling found (need Xcode's devicectl, or libimobiledevice)" >&2; exit 1; }

# Print one compact JSON object per attached device: {udid, name, model, os}.
list_devicectl() {
  local out; out="$(mktemp)"
  xcrun devicectl list devices --quiet --json-output "$out" >/dev/null 2>&1
  jq -c --arg tr "$TRANSPORTS" '
    ($tr | split(" ") | map(select(. != ""))) as $ok
    | .result.devices[]?
    | select(.hardwareProperties.reality == "physical")
    | select(.connectionProperties.pairingState == "paired")
    | select(.connectionProperties.transportType as $t | $t != null and ($ok | index($t)))
    | {udid: .hardwareProperties.udid, name: .deviceProperties.name,
       model: (.hardwareProperties.marketingName // .hardwareProperties.productType),
       os: .deviceProperties.osVersionNumber}' "$out" 2>/dev/null
  rm -f "$out"
}

list_libimobiledevice() {
  local udid
  for udid in $(idevice_id -l 2>/dev/null); do
    jq -nc --arg u "$udid" \
       --arg n "$(ideviceinfo -u "$udid" -k DeviceName 2>/dev/null)" \
       --arg m "$(ideviceinfo -u "$udid" -k ProductType 2>/dev/null)" \
       --arg o "$(ideviceinfo -u "$udid" -k ProductVersion 2>/dev/null)" \
       '{udid:$u, name:$n, model:$m, os:$o}'
  done
}

announce_once() {
  local dev body n=0
  while read -r dev; do
    [ -n "$dev" ] || continue
    body="$(jq -c --arg h "$HOST" --argjson mh "$MAX_HOLD" '
      {id: ("ios:" + .udid), kind: "ios-device",
       name: (if (.model // "") != "" then .model else .udid end),
       host: $h, max_hold_seconds: $mh,
       meta: {udid: .udid, model: (.model // ""), device_name: (.name // ""), os: (.os // "")}}' <<<"$dev")"
    curl -sf --max-time 5 -H "X-Registry-Token: $TOK" "$URL/resources/register" \
         -d "$body" >/dev/null 2>&1 && n=$((n+1))
  done < <("list_$TOOL")
  echo "announced $n device(s) to $URL (via $TOOL)"
}

command -v jq >/dev/null 2>&1 || { echo "jq required" >&2; exit 1; }

if [ "${1:-}" = "--loop" ]; then
  while :; do announce_once; sleep "$INTERVAL"; done
else
  announce_once
fi

#!/usr/bin/env bash
# Installs the agistry resource providers ONCE PER HOST into ~/.config/agistry/providers,
# and runs the ones named in AGISTRY_PROVIDERS as user services:
#   macOS  a launchd LaunchAgent per provider  (~/Library/LaunchAgents/dev.agistry.provider.<name>.plist)
#   Linux  a systemd user unit per provider    (agistry-provider@<name>)
# so they start now, at every login/boot, and restart if they die — independent of any
# agent session. Providers no longer named are stopped and removed. Re-run after
# changing AGISTRY_PROVIDERS; the Claude Code and Codex installers run it too.
#
# Usage: ./install.sh [--uninstall]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CFG="$HOME/.config/agistry/client.env"
DEST="$HOME/.config/agistry/providers"
UNINSTALL=0
case "${1:-}" in
  "") ;;
  --uninstall) UNINSTALL=1 ;;
  -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
  *) echo "unknown arg: $1" >&2; exit 1 ;;
esac

WANT=""
if [ "$UNINSTALL" = 0 ]; then
  WANT="$( ( [ -f "$CFG" ] && . "$CFG"; printf '%s' "${AGISTRY_PROVIDERS:-}" ) | tr ',' ' ')"
fi
wanted() { case " $WANT " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }

# Older versions ran providers as session-started daemons tracked by a pidfile. Stop
# those, or they would announce alongside the service.
for pf in "${XDG_RUNTIME_DIR:-${TMPDIR:-/tmp}}"/agistry-provider-*.pid; do
  [ -f "$pf" ] || continue
  p="$(cat "$pf" 2>/dev/null)"
  # only if that pid is still one of ours, not a reused pid
  if [ -n "$p" ] && ps -o command= -p "$p" 2>/dev/null | grep -q 'agistry-.*-provider\.sh'; then
    kill -TERM -- "-$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null || true
    echo "  stopped session-started provider (pid $p)"
  fi
  rm -f "$pf"
done

if [ "$UNINSTALL" = 0 ]; then
  mkdir -p "$DEST"
  for f in "$HERE"/agistry-*.sh; do install -m 0755 "$f" "$DEST/$(basename "$f")"; done
  install -m 0644 "$HERE/agistry-provider@.service" "$DEST/agistry-provider@.service"
  echo "  providers -> $DEST"
fi

case "$(uname -s)" in
  Darwin)
    AGENTS="$HOME/Library/LaunchAgents"; LOGS="$HOME/Library/Logs/agistry"
    DOMAIN="gui/$(id -u)"
    mkdir -p "$AGENTS" "$LOGS"
    # Drop agents for providers no longer wanted (all of them on --uninstall).
    for plist in "$AGENTS"/dev.agistry.provider.*.plist; do
      [ -f "$plist" ] || continue
      n="${plist##*/dev.agistry.provider.}"; n="${n%.plist}"
      wanted "$n" && continue
      launchctl bootout "$DOMAIN/dev.agistry.provider.$n" 2>/dev/null || true
      rm -f "$plist"; echo "  provider $n: stopped and removed"
    done
    for n in $WANT; do
      s="$DEST/agistry-$n-provider.sh"
      [ -x "$s" ] || { echo "  provider $n: unknown (no $s)" >&2; continue; }
      label="dev.agistry.provider.$n"; plist="$AGENTS/$label.plist"
      # launchd gives a bare PATH; add Homebrew's so jq is found.
      cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array><string>/bin/bash</string><string>$s</string><string>--loop</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>StandardOutPath</key><string>$LOGS/provider-$n.log</string>
  <key>StandardErrorPath</key><string>$LOGS/provider-$n.log</string>
</dict>
</plist>
EOF
      # bootout + bootstrap also restarts it, so a re-install runs the new script.
      launchctl bootout "$DOMAIN/$label" 2>/dev/null || true
      if launchctl bootstrap "$DOMAIN" "$plist"; then
        echo "  provider $n: running (launchd $label, log $LOGS/provider-$n.log)"
      else
        echo "  provider $n: launchctl bootstrap FAILED for $plist" >&2
      fi
    done
    ;;
  Linux)
    if ! command -v systemctl >/dev/null 2>&1 || ! systemctl --user show-environment >/dev/null 2>&1; then
      [ -n "$WANT" ] && echo "  no systemd user manager: run providers yourself, e.g. $DEST/agistry-<name>-provider.sh --loop" >&2
    else
      UNITS="$HOME/.config/systemd/user"
      for link in "$UNITS"/default.target.wants/agistry-provider@*.service; do
        [ -e "$link" ] || [ -L "$link" ] || continue
        n="${link##*/agistry-provider@}"; n="${n%.service}"
        wanted "$n" && continue
        systemctl --user disable --now "agistry-provider@$n" >/dev/null 2>&1 || true
        echo "  provider $n: stopped and disabled"
      done
      if [ "$UNINSTALL" = 1 ]; then
        rm -f "$UNITS/agistry-provider@.service"
      else
        mkdir -p "$UNITS"
        install -m 0644 "$DEST/agistry-provider@.service" "$UNITS/agistry-provider@.service"
      fi
      systemctl --user daemon-reload
      for n in $WANT; do
        [ -x "$DEST/agistry-$n-provider.sh" ] || { echo "  provider $n: unknown (no $DEST/agistry-$n-provider.sh)" >&2; continue; }
        systemctl --user enable "agistry-provider@$n" >/dev/null 2>&1
        # restart, not just start, so a re-install runs the new script
        if systemctl --user restart "agistry-provider@$n"; then
          echo "  provider $n: running (systemd agistry-provider@$n)"
        else
          echo "  provider $n: systemctl start FAILED — see: journalctl --user -u agistry-provider@$n" >&2
        fi
      done
      if [ -n "$WANT" ] && [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" != "yes" ]; then
        echo "  note: user services stop when you log out; to keep them running: loginctl enable-linger $(id -un)"
      fi
    fi
    ;;
  *)
    [ -n "$WANT" ] && echo "  unsupported OS for services: run $DEST/agistry-<name>-provider.sh --loop yourself" >&2
    ;;
esac

if [ "$UNINSTALL" = 1 ]; then
  rm -rf "$DEST"; echo "  removed $DEST"
elif [ -z "$WANT" ]; then
  echo "  no providers enabled (set AGISTRY_PROVIDERS in $CFG, then re-run $HERE/install.sh)"
fi

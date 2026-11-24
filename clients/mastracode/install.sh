#!/usr/bin/env bash
# Installs the agistry Mastra Code client: the plugin that registers each Mastra Code
# session and delivers agistry mail into it, plus the shared pieces (config, agistry
# skill, in-process client library, device providers). Safe to re-run.
#
# Usage: ./install.sh [--url URL] [--token TOKEN]
#        ./install.sh --uninstall
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGINS="$HOME/.mastracode/plugins"
DIR="$PLUGINS/agistry"
REG="$PLUGINS/plugins.json"

# Add or drop the plugin's record in Mastra Code's global plugin registry, keeping
# every other plugin and the disabled list as they are.
registry() {
  python3 - "$REG" "$1" "$DIR" <<'PY'
import json, os, sys
path, action, plugin_dir = sys.argv[1:]
try:
    with open(path) as f: reg = json.load(f)
except (OSError, ValueError): reg = {}
reg.setdefault("plugins", {}); reg.setdefault("disabledPlugins", [])
if action == "add":
    reg["plugins"]["agistry"] = {"enabled": True, "source": "local", "specifier": plugin_dir,
                                 "path": plugin_dir, "entry": "index.ts"}
else:
    reg["plugins"].pop("agistry", None)
os.makedirs(os.path.dirname(path), exist_ok=True)
tmp = path + ".tmp"
with open(tmp, "w") as f: json.dump(reg, f, indent=2); f.write("\n")
os.replace(tmp, path)
PY
}

case "${1:-}" in
  --uninstall)
    [ -f "$REG" ] && registry remove
    rm -rf "$DIR"; echo "  removed $DIR and its entry in $REG"
    echo "  left the shared skill, library, config and providers (other agents may use them)"
    exit 0 ;;
  -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
esac

command -v mastracode >/dev/null 2>&1 || echo "  WARNING: mastracode not found on PATH" >&2
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

echo "Installing agistry Mastra Code client"
"$HERE/../shared/install.sh" "$@"
mkdir -p "$DIR"
install -m 0644 "$HERE/agistry.ts" "$DIR/index.ts"
printf '{ "name": "agistry-mastracode", "private": true, "type": "module" }\n' > "$DIR/package.json"
registry add
echo "  plugin -> $DIR (enabled in $REG)"
echo
echo "Done. Start mastracode (or reload plugins with /plugins); the session appears in the dashboard."

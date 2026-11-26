#!/usr/bin/env bash
# Build agistry. Installs a local Go SDK if the box has none (no sudo — Go lands
# under ./.gosdk). For machines that already have a recent Go, just use `make build`.
set -euo pipefail
cd "$(dirname "$0")"

# modernc.org/sqlite tracks recent Go releases. Accept go1.25+ from system or a
# prior local SDK; otherwise fetch the latest.
NEW_GO_RE='go1\.(2[5-9]|[3-9][0-9])'

GO="$(command -v go || true)"
[ -n "$GO" ] && ! "$GO" version | grep -qE "$NEW_GO_RE" && GO=""
[ -z "$GO" ] && [ -x "$PWD/.gosdk/go/bin/go" ] && "$PWD/.gosdk/go/bin/go" version | grep -qE "$NEW_GO_RE" && GO="$PWD/.gosdk/go/bin/go"

if [ -z "$GO" ]; then
  GOVER="$(curl -fsSL 'https://go.dev/VERSION?m=text' 2>/dev/null | head -n1 | sed 's/^go//')"
  [ -z "$GOVER" ] && GOVER=1.25.4
  echo "Suitable Go not found — fetching a local copy (Go ${GOVER})..."
  case "$(uname -m)" in
    aarch64|arm64) GOARCH=arm64 ;;
    x86_64|amd64)  GOARCH=amd64 ;;
    *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
  esac
  TARBALL="go${GOVER}.linux-${GOARCH}.tar.gz"
  mkdir -p .gosdk
  rm -rf .gosdk/go
  curl -fsSL "https://go.dev/dl/${TARBALL}" -o ".gosdk/${TARBALL}"
  tar -C .gosdk -xzf ".gosdk/${TARBALL}"
  GO="$PWD/.gosdk/go/bin/go"
fi

export GOTOOLCHAIN=local
echo "Using: $("$GO" version)"

"$GO" mod tidy
"$GO" test ./...
CGO_ENABLED=0 "$GO" build -buildvcs=false -o agistry .

echo
echo "Built ./agistry"
echo "Run:  REGISTRY_TOKEN=dev ./agistry   then open http://127.0.0.1:7070/"

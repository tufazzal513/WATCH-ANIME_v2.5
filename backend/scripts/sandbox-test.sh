#!/usr/bin/env bash
# Runs the Go tests against a REAL SQLite in a sandbox where proxy.golang.org / modernc.org / golang.org are blocked
# but github.com is reachable. It copies backend/ to a scratch dir and swaps the SQLite driver for
# github.com/ncruces/go-sqlite3 (pure Go, same SQL). The repo itself is never modified.
# On a normal machine just run:  go test -race ./...
#
# usage: scripts/sandbox-test.sh [go test args...]     e.g.  scripts/sandbox-test.sh -race ./internal/repo/
set -euo pipefail
SRC="$(cd "$(dirname "$0")/.." && pwd)"
TMP="${SANDBOX_DIR:-/tmp/movex-sandbox}"
rm -rf "$TMP" && mkdir -p "$TMP" && cp -r "$SRC"/. "$TMP"/ && cd "$TMP"
export GOPROXY=direct GOSUMDB=off GOFLAGS=-mod=mod
rm -f internal/db/driver_modernc.go
cat > internal/db/driver_scratch.go <<'GO'
package db

import (
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)
GO
sed -i 's/const DriverName = "sqlite"/const DriverName = "sqlite3"/' internal/db/db.go
go mod edit -require=github.com/ncruces/go-sqlite3@v0.36.0 \
  -replace=golang.org/x/sys=github.com/golang/sys@v0.49.0 \
  -replace=golang.org/x/sync=github.com/golang/sync@v0.24.0 \
  -replace=golang.org/x/text=github.com/golang/text@v0.37.0 2>/dev/null || true
# resolve whatever versions the mirrors really have if the pinned ones do not exist
if ! go list ./... >/dev/null 2>&1; then
  for m in sys sync text; do v=$(git ls-remote --tags https://github.com/golang/$m 2>/dev/null | awk '{print $2}' | grep -E 'refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' | sed 's|refs/tags/||' | sort -V | tail -1); [ -n "$v" ] && go mod edit -replace=golang.org/x/$m=github.com/golang/$m@$v; done
fi
if [ $# -eq 0 ]; then set -- ./...; fi
go test -count=1 "$@"

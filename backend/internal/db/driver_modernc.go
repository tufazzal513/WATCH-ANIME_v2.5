//go:build !nodriver

package db

// Pure-Go SQLite driver (no CGO). Build with `-tags nodriver` only in sandboxes that cannot download modules.
import _ "modernc.org/sqlite"

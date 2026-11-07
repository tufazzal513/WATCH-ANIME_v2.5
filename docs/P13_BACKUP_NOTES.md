# P13 backup implementation notes

## Implemented in this part

- `backend/internal/backup` owns the backup orchestration and has no dependency on a live Telegram account.
- Creates a consistent SQLite snapshot with `VACUUM INTO`, outside a write transaction.
- Counts the application tables, computes SHA-256 over the raw snapshot, gzip-compresses it, and publishes `movex-YYYYMMDD-HHMMSS.db.gz` atomically.
- Uploads through the `TelegramClient` interface, pins the new message, unpins/deletes messages outside `TG_KEEP`, and stores a local atomic manifest.
- Retries transient failures with bounded exponential backoff; `FloodWaitError` extends the wait to Telegram's requested duration.
- Serializes manual/scheduled runs with a mutex and includes a context-cancellable scheduler.
- Tests use a fake SQL driver and mock Telegram transport, so they do not require credentials or a live channel.

## Still required before P13 can be marked DONE

The actual `github.com/gotd/td` MTProto adapter is not included yet. The current execution environment cannot resolve `github.com` and cannot download Go modules (`go get github.com/gotd/td@v0.120.0` failed with DNS resolution error). No Bot API fallback was substituted because the project specification requires MTProto user-session support and large document uploads.

The adapter must implement `backup.TelegramClient` using the authorized user session stored at `DATA_DIR/telegram.session`, resolve `TG_CHAT_ID`, upload the gzip document with the supplied caption, pin/unpin/delete messages, and map Telegram flood-wait errors to `backup.FloodWaitError`. Do not store the session in SQLite or in the backup archive.

The scheduler is provided but is not started by `main` until the gotd adapter and session lifecycle are wired. That prevents the server from pretending Telegram backup is active when no authenticated transport exists.

## Follow-up: secure session persistence primitive

`internal/backup.SessionFileStore` is now available for the future gotd storage adapter. It uses a private `0600` file, atomic same-directory writes, a `0700` parent directory, symlink rejection, and a 4 MiB upper bound. It is intentionally transport-agnostic and does not itself authenticate with Telegram. The actual gotd adapter and login lifecycle are still blocked until the Go module can be fetched in a network-enabled build environment.

## P14 continuation note (2026-11-06)

A read-only `/admin/server` page has been added. It renders only `Config.Summary()` (secret values are represented as set/not set), reports whether the protected session file exists, and lists up to 20 local compressed backups. It explicitly states that Telegram operations are disabled. The gotd dependency remains unavailable due DNS failure, so neither Telegram login nor restore-from-Telegram is claimed to work.

## Retry on 2026-11-07

- Re-ran `go test -race -tags nodriver ./...`: PASS for every package.
- Re-ran `go vet -tags nodriver ./...`: PASS.
- Retried `GOPROXY=https://proxy.golang.org go get github.com/gotd/td@v0.120.0`: BLOCKED before download because DNS lookup for `proxy.golang.org` is refused in this execution environment (`lookup ...: connection refused`).
- No Bot API substitution or fake MTProto adapter was added. The real user-session transport cannot be honestly implemented or verified without the gotd module source and its transitive dependencies.
- P13 remains PARTIAL. The next concrete action is to run the dependency fetch in a network-enabled Go environment, then implement and test the gotd adapter against v0.120.0 before enabling the scheduler.

## Real gotd adapter source added (2026-11-07, build still pending)

`internal/telegrammtproto` now contains a gotd v0.120.0 user-session adapter implementing both `backup.TelegramClient` and `backup.LoginTransport`. It requests a phone code, supports the optional cloud password, stores the authorized gotd session in `DATA_DIR/telegram.session`, uploads the compressed snapshot with the manifest caption, extracts the returned message ID, and implements pin/unpin/delete. Numeric `-100...` channel IDs are looked up in the logged-in account's dialogs to obtain the channel access hash; `@username` is resolved through gotd's peer resolver. FLOOD_WAIT RPC errors are mapped to the backup retry contract.

The protected `/admin/server` page now exposes the phone/code/2FA flow, and the main server starts the configured backup scheduler when TG settings are present. The scheduler's first run is delayed by the configured interval and failures retry at the next interval.

This is implemented source, not a verified release: the current sandbox cannot fetch gotd v0.120.0 or its transitive modules, so the new adapter cannot yet be compiled or tested. Do not mark P13 DONE until a network-enabled build/test and a real Telegram channel smoke test pass. The admin CSS source fingerprint also needs regeneration with `backend/internal/admin/build-css.sh` in an npm-enabled environment.

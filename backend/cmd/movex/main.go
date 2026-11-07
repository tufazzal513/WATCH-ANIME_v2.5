// Command movex is the MoveX backend: user API, admin panel, backups and replication in one binary.
//
//	movex            run the server (same as "movex serve")
//	movex version    print the version
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"movex/internal/auth"
	"movex/internal/backup"
	"movex/internal/config"
	"movex/internal/db"
	"movex/internal/presence"
	"movex/internal/repo"
	"movex/internal/server"
	"movex/internal/telegrammtproto"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version", "-v", "--version":
		fmt.Println("movex", server.Version)
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "movex: "+err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (use: serve, version)\n", cmd)
		os.Exit(2)
	}
}

func serve() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create DATA_DIR %q: %w", cfg.DataDir, err)
	}
	if cfg.IsMain() {
		if err := cfg.ResolveSessionSecret(); err != nil {
			return err
		}
	}
	if cfg.DevInsecureAuth {
		log.Warn("DEV_INSECURE_AUTH is ON: dev:<uid> tokens are accepted. Never use this in production.")
	}
	if len(cfg.AllowedOrigins) == 1 && cfg.AllowedOrigins[0] == "*" {
		log.Warn("ALLOWED_ORIGINS is not set: any website may call the API. Set it to your frontend address.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.IsMain() {
		pendingApplied, restoreErr := backup.ApplyPendingRestore(ctx, cfg.DataDir, cfg.DBPath(), backup.RestoreOptions{
			CurrentSchema:  4,
			RequiredTables: []string{"schema_migrations", "meta", "movies", "users", "workers"},
			OpenSnapshot:   func(ctx context.Context, path string) (*sql.DB, error) { return db.Open(ctx, path) },
			// At startup no application DB handles exist yet. Open the previous DB
			// only after the incoming snapshot validates, checkpoint any crash-left
			// WAL, then close it before the atomic file replacement.
			BeforeReplace: func() error {
				if _, statErr := os.Stat(cfg.DBPath()); os.IsNotExist(statErr) {
					return nil
				} else if statErr != nil {
					return statErr
				}
				oldDB, openErr := db.Open(ctx, cfg.DBPath())
				if openErr != nil {
					return openErr
				}
				var busy, logFrames, checkpointed int
				checkpointErr := oldDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed)
				closeErr := oldDB.Close()
				if checkpointErr != nil {
					return checkpointErr
				}
				if busy != 0 {
					return fmt.Errorf("SQLite WAL checkpoint is busy (%d log frames, %d checkpointed)", logFrames, checkpointed)
				}
				return closeErr
			},
			AfterReplace: func() error { return nil },
		})
		if restoreErr != nil {
			return fmt.Errorf("pending database restore: %w", restoreErr)
		}
		if !pendingApplied && cfg.TelegramConfigured() {
			empty, emptyErr := backup.IsDatabaseFileEmpty(ctx, cfg.DBPath())
			if emptyErr != nil {
				return fmt.Errorf("check database before Telegram first-boot restore: %w", emptyErr)
			}
			if empty {
				tgClient, tgErr := telegrammtproto.New(telegrammtproto.Config{
					APIID: cfg.TGAPIID, APIHash: cfg.TGAPIHash, ChatID: cfg.TGChatID,
					SessionPath: cfg.TelegramSession(),
				})
				if tgErr != nil {
					return fmt.Errorf("initialize Telegram restore client: %w", tgErr)
				}
				remote, remoteErr := backup.RestoreLatestFromTelegram(ctx, tgClient, backup.RestoreOptions{
					DBPath: cfg.DBPath(), TempDir: cfg.DataDir, CurrentSchema: 4,
					RequiredTables: []string{"schema_migrations", "meta", "movies", "users", "workers"},
					OpenSnapshot:   func(ctx context.Context, path string) (*sql.DB, error) { return db.Open(ctx, path) },
					IsDatabaseEmpty: func(checkCtx context.Context) (bool, error) {
						return backup.IsDatabaseFileEmpty(checkCtx, cfg.DBPath())
					},
				})
				if remoteErr != nil {
					return fmt.Errorf("automatic Telegram database restore: %w", remoteErr)
				}
				log.Info("database restored from Telegram backup", "message_id", remote.MessageID, "file", remote.Filename, "backup_at", remote.Metadata.At)
			}
		}
	}
	octx, cancel := context.WithTimeout(ctx, 30*time.Second)
	database, err := db.Open(octx, cfg.DBPath())
	if err == nil {
		var applied []int
		applied, err = db.Migrate(octx, database)
		if err == nil && len(applied) > 0 {
			log.Info("migrations applied", "versions", applied)
		}
	}
	cancel()
	if err != nil {
		return err
	}
	defer database.Close()

	rp := repo.New(database)
	var backupManager *backup.Manager
	var telegramLogin *backup.LoginFlow
	if cfg.IsMain() {
		var telegramClient backup.TelegramClient
		if cfg.TelegramConfigured() {
			tgClient, tgErr := telegrammtproto.New(telegrammtproto.Config{
				APIID: cfg.TGAPIID, APIHash: cfg.TGAPIHash, ChatID: cfg.TGChatID,
				SessionPath: cfg.TelegramSession(),
			})
			if tgErr != nil {
				return fmt.Errorf("initialize Telegram MTProto: %w", tgErr)
			}
			telegramClient = tgClient
			telegramLogin, err = backup.NewLoginFlow(func(context.Context) (backup.LoginTransport, error) {
				return telegrammtproto.New(telegrammtproto.Config{APIID: cfg.TGAPIID, APIHash: cfg.TGAPIHash, ChatID: cfg.TGChatID, SessionPath: cfg.TelegramSession()})
			}, backup.SessionFileStore{Path: cfg.TelegramSession()}, 10*time.Minute)
			if err != nil {
				return fmt.Errorf("initialize Telegram login flow: %w", err)
			}
		}
		backupManager, err = backup.New(backup.Options{DB: database, DBPath: cfg.DBPath(), Dir: cfg.DataDir + "/backups", Keep: cfg.TGKeep, SchemaVersion: 4, Telegram: telegramClient, Logger: slog.Default()})
		if err != nil {
			return fmt.Errorf("initialize backup manager: %w", err)
		}
	}
	if cfg.IsMain() { // workers receive the genre list through replication
		sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
		n, serr := rp.SeedCategories(sctx)
		scancel()
		if serr != nil {
			return fmt.Errorf("seed categories: %w", serr)
		}
		if n > 0 {
			log.Info("default categories created", "count", n)
		}
	}

	srv := server.New(server.Deps{
		Cfg: cfg, DB: database, Log: log, Presence: presence.New(0), Repo: rp, Seq: rp.SeqFunc(),
		Verifier: auth.NewVerifier(auth.Options{ProjectID: cfg.FirebaseProjectID, DevInsecure: cfg.DevInsecureAuth}), Backup: backupManager, TelegramLogin: telegramLogin,
	})
	return srv.Run(ctx)
}

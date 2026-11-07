package admin

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"movex/internal/backup"
	"time"
)

type serverPageData struct {
	Config             []serverConfigItem
	Backups            []backupFileItem
	TelegramConfigured bool
	SessionExists      bool
	BackupDir          string
	BackupAvailable    bool
	PendingRestore     bool
	BackupRunning      bool
	LastBackupError    string
	LastBackupFile     string
}

type serverConfigItem struct{ Key, Value string }
type backupFileItem struct {
	Name string
	Size int64
}

// serverPage deliberately exposes only Config.Summary values. Secret values are never rendered.
func (a *Admin) serverPage(c *Ctx) {
	_, pendingErr := os.Stat(filepath.Join(a.deps.Cfg.DataDir, "restore.pending.db.gz"))
	data := serverPageData{PendingRestore: pendingErr == nil, BackupAvailable: a.deps.Backup != nil, TelegramConfigured: a.deps.Cfg.TelegramConfigured(), SessionExists: false, BackupDir: filepath.Join(a.deps.Cfg.DataDir, "backups")}
	for k, v := range a.deps.Cfg.Summary() {
		data.Config = append(data.Config, serverConfigItem{Key: k, Value: v})
	}
	sort.Slice(data.Config, func(i, j int) bool { return data.Config[i].Key < data.Config[j].Key })
	if a.deps.Backup != nil {
		var last *backup.Result
		data.BackupRunning, last, data.LastBackupError = a.deps.Backup.Status()
		if last != nil {
			data.LastBackupFile = last.Filename
		}
	}
	if st, err := os.Lstat(a.deps.Cfg.TelegramSession()); err == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0 {
		data.SessionExists = true
	}
	entries, err := os.ReadDir(data.BackupDir)
	if err == nil {
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".db.gz") {
				continue
			}
			st, e := ent.Info()
			if e != nil || !st.Mode().IsRegular() {
				continue
			}
			data.Backups = append(data.Backups, backupFileItem{Name: ent.Name(), Size: st.Size()})
		}
	}
	sort.Slice(data.Backups, func(i, j int) bool { return data.Backups[i].Name > data.Backups[j].Name })
	if len(data.Backups) > 20 {
		data.Backups = data.Backups[:20]
	}
	c.Render("server", Page{Title: "Server", Active: "server", Data: data})
}

// createLocalBackup creates a local SQLite snapshot and uploads it to Telegram when MTProto is configured.
func (a *Admin) createLocalBackup(c *Ctx) {
	if a.deps.Backup == nil {
		c.Flash("error", "Local backup is not available on this server.")
		c.Redirect("/admin/server")
		return
	}
	ctx, cancel := context.WithTimeout(c.R.Context(), 45*time.Second)
	defer cancel()
	result, err := a.deps.Backup.Run(ctx)
	if err != nil {
		a.deps.Log.Error("manual local backup failed", "err", err)
		c.Flash("error", "Local backup failed. Check the server log for details.")
		c.Redirect("/admin/server")
		return
	}
	if result.TelegramMessageID > 0 {
		c.Flash("success", "Snapshot created and uploaded to Telegram: "+result.Filename)
	} else {
		c.Flash("success", "Local snapshot created: "+result.Filename)
	}
	c.Redirect("/admin/server")
}

var localBackupName = regexp.MustCompile(`^movex-[0-9]{8}-[0-9]{6}\.db\.gz$`)

// downloadLocalBackup serves only regular snapshot files created by the local backup manager.
// It deliberately accepts a strict basename, never an arbitrary filesystem path.
func (a *Admin) downloadLocalBackup(c *Ctx) {
	name := c.R.PathValue("name")
	if !localBackupName.MatchString(name) || filepath.Base(name) != name {
		http.NotFound(c.W, c.R)
		return
	}
	dir := filepath.Join(a.deps.Cfg.DataDir, "backups")
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		http.NotFound(c.W, c.R)
		return
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		http.NotFound(c.W, c.R)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(c.W, c.R)
		return
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		http.NotFound(c.W, c.R)
		return
	}
	c.Audit("backup.download", name, "local SQLite snapshot")
	c.W.Header().Set("Content-Type", "application/gzip")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	c.W.Header().Set("Content-Length", strconv.FormatInt(opened.Size(), 10))
	http.ServeContent(c.W, c.R, name, opened.ModTime(), f)
}

// telegramLoginBegin starts the MTProto phone-code flow. The challenge remains
// in process memory and expires automatically; secrets are never written to logs.
func (a *Admin) telegramLoginBegin(c *Ctx) {
	if a.deps.TelegramLogin == nil {
		c.Flash("error", "Telegram MTProto is not configured on this server.")
		c.Redirect("/admin/server")
		return
	}
	ctx, cancel := context.WithTimeout(c.R.Context(), 45*time.Second)
	defer cancel()
	if err := a.deps.TelegramLogin.Begin(ctx, c.R.FormValue("phone")); err != nil {
		a.deps.Log.Warn("Telegram login code request failed", "err", err)
		c.Flash("error", "Could not request a Telegram login code. Check the phone number and server logs.")
		c.Redirect("/admin/server")
		return
	}
	c.Audit("telegram.login.begin", "", "Telegram login code requested")
	c.Flash("success", "Telegram login code requested. Enter the code sent by Telegram.")
	c.Redirect("/admin/server")
}

func (a *Admin) telegramLoginCode(c *Ctx) {
	if a.deps.TelegramLogin == nil {
		c.Flash("error", "Telegram MTProto is not configured on this server.")
		c.Redirect("/admin/server")
		return
	}
	ctx, cancel := context.WithTimeout(c.R.Context(), 45*time.Second)
	defer cancel()
	if err := a.deps.TelegramLogin.SubmitCode(ctx, c.R.FormValue("code")); err != nil {
		a.deps.Log.Warn("Telegram login code verification failed", "err", err)
		c.Flash("error", "Telegram code was not accepted, or a 2FA password is required.")
		c.Redirect("/admin/server")
		return
	}
	c.Audit("telegram.login.complete", "", "Telegram session authorized")
	c.Flash("success", "Telegram account authorized and session saved securely.")
	c.Redirect("/admin/server")
}

func (a *Admin) telegramLoginPassword(c *Ctx) {
	if a.deps.TelegramLogin == nil {
		c.Flash("error", "Telegram MTProto is not configured on this server.")
		c.Redirect("/admin/server")
		return
	}
	ctx, cancel := context.WithTimeout(c.R.Context(), 45*time.Second)
	defer cancel()
	if err := a.deps.TelegramLogin.SubmitPassword(ctx, c.R.FormValue("password")); err != nil {
		a.deps.Log.Warn("Telegram 2FA verification failed", "err", err)
		c.Flash("error", "Telegram 2FA password was not accepted.")
		c.Redirect("/admin/server")
		return
	}
	c.Audit("telegram.login.complete", "", "Telegram session authorized with 2FA")
	c.Flash("success", "Telegram account authorized and session saved securely.")
	c.Redirect("/admin/server")
}

func (a *Admin) telegramLoginCancel(c *Ctx) {
	if a.deps.TelegramLogin != nil {
		if err := a.deps.TelegramLogin.Cancel(); err != nil {
			a.deps.Log.Warn("Telegram login cancellation failed", "err", err)
		}
	}
	c.Audit("telegram.login.cancel", "", "Pending Telegram login cancelled")
	c.Flash("success", "Pending Telegram login cancelled.")
	c.Redirect("/admin/server")
}

// stageRestore accepts a compressed SQLite archive but never replaces the live database.
// The validated archive is applied before the next process opens SQLite.
func (a *Admin) stageRestore(c *Ctx) {
	if c.R.Method != http.MethodPost {
		c.Error(http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if err := c.R.ParseMultipartForm(1 << 20); err != nil {
		c.Flash("error", "Could not read restore upload. The archive may be too large or invalid.")
		c.Redirect("/admin/server")
		return
	}
	if c.R.FormValue("confirmation") != "RESTORE" {
		c.Flash("error", "Type RESTORE exactly to stage a database restore.")
		c.Redirect("/admin/server")
		return
	}
	f, header, err := c.R.FormFile("archive")
	if err != nil {
		c.Flash("error", "Choose a .db.gz backup file first.")
		c.Redirect("/admin/server")
		return
	}
	defer f.Close()
	if !strings.HasSuffix(strings.ToLower(filepath.Base(header.Filename)), ".db.gz") {
		c.Flash("error", "Only .db.gz backup archives are accepted.")
		c.Redirect("/admin/server")
		return
	}
	path, err := backup.StageRestoreArchive(a.deps.Cfg.DataDir, f)
	if err != nil {
		a.deps.Log.Warn("restore archive staging failed", "err", err)
		c.Flash("error", "Restore archive could not be staged. Check the file and server logs.")
		c.Redirect("/admin/server")
		return
	}
	c.Audit("backup.restore.stage", filepath.Base(path), "manual restore scheduled for next restart")
	c.Flash("success", "Backup archive staged. It will be fully validated and restored on the next server restart, before the database opens.")
	c.Redirect("/admin/server")
}

func (a *Admin) cancelRestore(c *Ctx) {
	path := filepath.Join(a.deps.Cfg.DataDir, "restore.pending.db.gz")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		c.Flash("error", "Could not remove pending restore archive.")
	} else {
		c.Audit("backup.restore.cancel", "", "pending restore archive removed")
		c.Flash("success", "Pending restore cancelled.")
	}
	c.Redirect("/admin/server")
}

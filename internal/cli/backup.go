package cli

import (
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/admin"
	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	commands = append(commands,
		&command{
			Name: "backup", Args: "[file]", Group: groupAcct,
			Summary: "Download a full backup of the server (admins)",
			Help: `Downloads a consistent backup (database + files + secret.key) from the
server you're signed in to and saves it as a .tar.gz. Keep it safe: with the
key inside it can decrypt stored secrets.

  bunker backup                         # ./bunker-backup-<time>.tar.gz
  bunker backup /backups/today.tar.gz
  bunker backup --no-key                # leave secret.key out`,
			Flags: []flagDoc{{Name: "no-key", Desc: "Leave secret.key out (encrypted settings won't restore)"}},
			Run:   cmdBackup,
		},
		&command{
			Name: "restore", Args: "<backup.tar.gz> | [site] [version]", Group: groupAcct,
			Summary: "Restore a server backup into BUNKER_DATA_DIR (server stopped)",
			Help: `Restores a backup made with "bunker backup" (or the dashboard) into this
machine's data folder (BUNKER_DATA_DIR, default /data or ./bunker-data). Stop
the server first. An existing database is kept next to it as
bunker.db.pre-restore-<time>.

  bunker restore bunker-backup-20261105-101500.tar.gz
  BUNKER_DATA_DIR=/srv/bunker bunker restore today.tar.gz --yes

Given a site (and version) instead of a file, it rolls the site back like
"bunker rollback".`,
			Run: cmdRestore,
		},
	)
}

func cmdBackup(x *App, a *Args) error {
	c, err := x.client("")
	if err != nil {
		return err
	}
	q := url.Values{}
	if a.Bool("no-key") {
		q.Set("includeKey", "false")
	}
	resp, err := c.Raw(x.ctx, "POST", "/api/v1/admin/backup", q, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.errorFrom(resp)
	}
	dst := a.Arg(0)
	if dst == "" {
		dst = "bunker-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
		if _, p, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && p["filename"] != "" {
			dst = filepath.Base(p["filename"])
		}
	}
	if st, err := os.Stat(dst); err == nil && st.IsDir() {
		dst = filepath.Join(dst, "bunker-backup-"+time.Now().UTC().Format("20060102-150405")+".tar.gz")
	}
	if _, err := os.Stat(dst); err == nil {
		if err := x.confirm(dst + " exists. Replace it?"); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".bunker-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("the download stopped early: %w", err)
	}
	os.Chmod(tmp.Name(), 0o600)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	return x.result(map[string]any{"file": dst, "bytes": n, "server": c.Server}, func() {
		x.ok("Saved a backup of %s to %s (%s)", c.Server, dst, humanBytes(n))
		if !a.Bool("no-key") {
			x.info("It includes secret.key, so keep it somewhere safe.")
		}
	})
}

// looksLikeBackup tells a backup file from a site name.
func looksLikeBackup(p string) bool {
	if strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz") {
		return true
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func cmdRestore(x *App, a *Args) error {
	if len(a.Pos) == 0 || !looksLikeBackup(a.Pos[0]) {
		return cmdRollback(x, a)
	}
	if len(a.Pos) > 1 {
		return usageErr("restore takes one backup file")
	}
	src := a.Pos[0]
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	cfg := core.LoadConfig()
	dir := cfg.DataDir
	if serverRunning(cfg.Listen) {
		return fmt.Errorf("a Bunker server is running on %s. Stop it first, then run the restore again", cfg.Listen)
	}
	db := filepath.Join(dir, "bunker.db")
	_, statErr := os.Stat(db)
	exists := statErr == nil
	q := "Restore " + src + " into " + dir + "?"
	if exists {
		q = dir + " already has a Bunker database. Replace it with " + src + "? (the current one is kept as bunker.db.pre-restore-<time>)"
	}
	if err := x.confirm(q); err != nil {
		return err
	}
	kept := ""
	if exists {
		kept = db + ".pre-restore-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(db, kept); err != nil {
			return err
		}
		for _, s := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(db + s); err == nil {
				os.Rename(db+s, kept+s)
			}
		}
	}
	if err := admin.RestoreArchive(f, dir, true); err != nil {
		if kept != "" {
			os.Rename(kept, db) // put the old database back
			for _, s := range []string{"-wal", "-shm"} {
				os.Rename(kept+s, db+s)
			}
		}
		return err
	}
	return x.result(map[string]any{"dataDir": dir, "file": src, "previous": kept}, func() {
		x.ok("Restored %s into %s", src, dir)
		if kept != "" {
			x.info("The previous database is kept as %s", kept)
		}
		x.info("Start the server again: bunker serve")
	})
}

// serverRunning reports whether something answers /healthz on listen.
func serverRunning(listen string) bool {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "8080"
	}
	c := &http.Client{Timeout: 700 * time.Millisecond}
	res, err := c.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

package admin

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// A backup is a tar.gz of the data directory with a consistent SQLite
// snapshot (VACUUM INTO) in place of the live database. Blobs are
// content-addressed and never change, so copying them while Bunker runs
// is safe. Restore = unpack into an empty data directory and start Bunker.

var backupMu sync.Mutex

// skipInBackup reports whether a data-dir path is excluded.
func skipInBackup(rel string, backupDir string) bool {
	rel = filepath.ToSlash(rel)
	switch rel {
	case "bunker.db", "bunker.db-wal", "bunker.db-shm", "setup-code.txt":
		return true
	}
	if strings.HasPrefix(rel, "blobs/tmp/") || rel == "blobs/tmp" || strings.HasPrefix(rel, "tmp/") || strings.HasPrefix(rel, "backups/") {
		return true
	}
	if strings.HasPrefix(rel, "blobs/") && (strings.HasSuffix(rel, ".gz") || strings.HasSuffix(rel, ".br")) {
		return true // compressed variants are rebuilt on demand
	}
	if backupDir != "" {
		abs := filepath.Join(app.Cfg.DataDir, filepath.FromSlash(rel))
		if r, err := filepath.Rel(backupDir, abs); err == nil && !strings.HasPrefix(r, "..") {
			return true
		}
	}
	return false
}

// WriteBackup streams a consistent backup to w.
func WriteBackup(ctx context.Context, w io.Writer, includeKey bool) (int, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	tmpDir, err := os.MkdirTemp(app.Cfg.DataDir, ".backup-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmpDir)
	snap := filepath.Join(tmpDir, "bunker.db")
	if _, err := app.DB.ExecContext(ctx, `VACUUM INTO ?`, snap); err != nil {
		return 0, fmt.Errorf("snapshot: %w", err)
	}
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	files := 0
	add := func(name, src string, info fs.FileInfo) error {
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = name
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.CopyN(tw, f, info.Size())
		files++
		return err
	}
	man, _ := json.MarshalIndent(map[string]any{"format": 1, "app": "bunker", "version": core.Version, "created": time.Now().UTC().Format(time.RFC3339), "includesKey": includeKey}, "", "  ")
	tw.WriteHeader(&tar.Header{Name: "bunker-backup.json", Mode: 0o600, Size: int64(len(man)), ModTime: time.Now()})
	tw.Write(man)
	st, err := os.Stat(snap)
	if err != nil {
		return 0, err
	}
	if err := add("bunker.db", snap, st); err != nil {
		return 0, err
	}
	bdir := backupDir()
	err = filepath.WalkDir(app.Cfg.DataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // file vanished (e.g. temp upload)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(app.Cfg.DataDir, p)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), ".backup-") || skipInBackup(rel, bdir) || (!includeKey && rel == "secret.key") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return add(filepath.ToSlash(rel)+"/", p, info)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := add(filepath.ToSlash(rel), p, info); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
	if err != nil {
		return files, err
	}
	if err := tw.Close(); err != nil {
		return files, err
	}
	return files, gz.Close()
}

// RestoreArchive unpacks a backup into dataDir (which must not already
// hold a database unless force is set). Used by "bunker restore" and tests.
func RestoreArchive(r io.Reader, dataDir string, force bool) error {
	if _, err := os.Stat(filepath.Join(dataDir, "bunker.db")); err == nil && !force {
		return errors.New(dataDir + " already has a Bunker database; restore into an empty folder")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return errors.New("this isn't a Bunker backup (not gzip)")
	}
	tr := tar.NewReader(gz)
	seenManifest := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("backup is damaged: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." || strings.HasPrefix(name, "../") || name == ".." || path.IsAbs(name) || strings.Contains(name, "\\") {
			return fmt.Errorf("unsafe path in backup: %s", h.Name)
		}
		if name == "bunker-backup.json" {
			seenManifest = true
			continue
		}
		dst := filepath.Join(dataDir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// links and devices are never part of a Bunker backup
		}
	}
	if !seenManifest {
		return errors.New("this isn't a Bunker backup (no bunker-backup.json)")
	}
	for _, s := range []string{"-wal", "-shm"} {
		os.Remove(filepath.Join(dataDir, "bunker.db"+s))
	}
	return nil
}

// ---- scheduled snapshots ----

func backupDir() string {
	d := app.Setting("backup.dir")
	if d == "" {
		return ""
	}
	if !filepath.IsAbs(d) {
		d = filepath.Join(app.Cfg.DataDir, d)
	}
	return filepath.Clean(d)
}

func backupEvery() time.Duration {
	d, err := time.ParseDuration(app.Setting("backup.every"))
	if err != nil || d <= 0 {
		d = 24 * time.Hour
	}
	return max(d, time.Hour)
}

func backupKeep() int {
	if n, err := strconv.Atoi(app.Setting("backup.keep")); err == nil && n > 0 {
		return n
	}
	return 7
}

type snapshot struct {
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Created int64  `json:"created"`
}

func listSnapshots(dir string) []snapshot {
	out := []snapshot{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "bunker-") || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		if info, err := e.Info(); err == nil {
			out = append(out, snapshot{Name: e.Name(), Bytes: info.Size(), Created: info.ModTime().Unix()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

// Snapshot writes a backup into the backup folder and prunes old ones.
func Snapshot(ctx context.Context) (snapshot, error) {
	dir := backupDir()
	if dir == "" {
		return snapshot{}, core.BadRequest("scheduled backups are off: set BUNKER_BACKUP_DIR (or backup.dir) to a folder")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return snapshot{}, err
	}
	name := "bunker-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return snapshot{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := WriteBackup(ctx, tmp, true); err != nil {
		tmp.Close()
		return snapshot{}, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return snapshot{}, err
	}
	tmp.Close()
	dst := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return snapshot{}, err
	}
	all := listSnapshots(dir)
	for i, s := range all {
		if i >= backupKeep() {
			os.Remove(filepath.Join(dir, s.Name))
		}
	}
	st, _ := os.Stat(dst)
	return snapshot{Name: name, Bytes: st.Size(), Created: st.ModTime().Unix()}, nil
}

func lastSnapshot(dir string) time.Time {
	if s := listSnapshots(dir); len(s) > 0 {
		return time.Unix(s[0].Created, 0)
	}
	return time.Time{}
}

// scheduleLoop writes a snapshot every BUNKER_BACKUP_EVERY (default 24h)
// into BUNKER_BACKUP_DIR, keeping BUNKER_BACKUP_KEEP (default 7).
func scheduleLoop(ctx context.Context, a *core.App) {
	wait := time.Minute // let startup settle first
	for {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
		wait = time.Hour // re-check settings at least hourly
		dir := backupDir()
		if dir == "" {
			continue
		}
		next := lastSnapshot(dir).Add(backupEvery())
		if d := time.Until(next); d > 0 {
			wait = min(d, time.Hour)
			continue
		}
		if s, err := Snapshot(ctx); err != nil {
			a.Log.Error("scheduled backup failed", "err", err)
			a.Audit(ctx, "admin.backup.failed", 0, dir, map[string]any{"error": err.Error()})
			wait = 15 * time.Minute
		} else {
			a.Log.Info("backup written", "file", s.Name, "bytes", s.Bytes)
			a.Audit(ctx, "admin.backup", 0, s.Name, map[string]any{"bytes": s.Bytes, "scheduled": true})
			wait = min(backupEvery(), time.Hour)
		}
	}
}

func registerBackup(a *core.App) {
	a.Handle(core.Op{Name: "admin.backup", Method: "POST", Path: "/api/v1/admin/backup", Access: core.AccessAdmin, Body: "none",
		Summary: "Download a consistent backup (database + files) as .tar.gz",
		Params:  []core.Param{{Name: "includeKey", In: "query", Type: "boolean", Desc: "Include secret.key so encrypted settings restore too (default true; keep the file safe)"}},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			includeKey := r.URL.Query().Get("includeKey") != "false"
			name := "bunker-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			w.Header().Set("Cache-Control", "no-store")
			a.Audit(r.Context(), "admin.backup", 0, name, map[string]any{"download": true, "includesKey": includeKey})
			if _, err := WriteBackup(r.Context(), w, includeKey); err != nil {
				a.Log.Error("backup download failed", "err", err)
				// Headers are gone; the truncated gzip stream fails to unpack, which is the signal.
			}
		}})
	a.Handle(core.Op{Name: "admin.backups.list", Method: "GET", Path: "/api/v1/admin/backups", Access: core.AccessAdmin, Summary: "Scheduled backups and their settings",
		Fn: func(r *http.Request) (any, error) {
			dir := backupDir()
			return map[string]any{"enabled": dir != "", "dir": dir, "every": backupEvery().String(), "keep": backupKeep(), "backups": listSnapshots(dir)}, nil
		}})
	a.Handle(core.Op{Name: "admin.backups.run", Method: "POST", Path: "/api/v1/admin/backups", Access: core.AccessAdmin, Body: "none", Summary: "Write a backup into the backup folder now",
		Fn: func(r *http.Request) (any, error) {
			s, err := Snapshot(r.Context())
			if err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "admin.backup", 0, s.Name, map[string]any{"bytes": s.Bytes})
			return core.Created{V: s}, nil
		}})
}

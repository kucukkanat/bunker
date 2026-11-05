package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// OpenDB opens (creating if needed) the SQLite database in WAL mode.
func OpenDB(dir string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "bunker.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

const coreSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (module TEXT NOT NULL, n INTEGER NOT NULL, applied_at INTEGER NOT NULL, PRIMARY KEY(module, n));
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY,
  subject TEXT UNIQUE NOT NULL,
  email TEXT NOT NULL,
  name TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  groups_json TEXT NOT NULL DEFAULT '[]',
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS users_email ON users(email);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  actor_id INTEGER,
  actor_email TEXT,
  event TEXT NOT NULL,
  site_id INTEGER,
  target TEXT,
  ip TEXT,
  meta TEXT
);
CREATE INDEX IF NOT EXISTS audit_ts ON audit(ts);
CREATE INDEX IF NOT EXISTS audit_site ON audit(site_id, ts);
CREATE INDEX IF NOT EXISTS audit_actor ON audit(actor_id, ts);
CREATE INDEX IF NOT EXISTS audit_event ON audit(event, ts);
`

// Migrate applies the core schema and every module's migrations.
func (a *App) Migrate(ctx context.Context) error {
	if _, err := a.DB.ExecContext(ctx, coreSchema); err != nil {
		return fmt.Errorf("core schema: %w", err)
	}
	for _, m := range Modules() {
		for i, stmt := range m.Migrations {
			n := i + 1
			var exists int
			_ = a.DB.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE module=? AND n=?`, m.Name, n).Scan(&exists)
			if exists == 1 {
				continue
			}
			tx, err := a.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s#%d: %w", m.Name, n, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(module,n,applied_at) VALUES(?,?,?)`, m.Name, n, time.Now().Unix()); err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Setting returns a runtime setting. Environment variables
// (BUNKER_<KEY>, with dots replaced by underscores) take precedence so
// infrastructure-as-code deployments can pin values.
func (a *App) Setting(key string) string {
	envKey := strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
	if v := Env(envKey, ""); v != "" {
		return v
	}
	var v string
	_ = a.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v
}

// SettingFromEnv reports whether a setting is pinned by the environment.
func (a *App) SettingFromEnv(key string) bool {
	envKey := strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
	return Env(envKey, "") != ""
}

// SetSetting stores a runtime setting.
func (a *App) SetSetting(ctx context.Context, key, value string) error {
	_, err := a.DB.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now().Unix())
	return err
}

// Now is the unix time in seconds (overridable in tests).
var Now = func() int64 { return time.Now().Unix() }

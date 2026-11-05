package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/selfupdate"
)

// Updates: Admin → Updates checks GitHub for releases (only when an admin
// looks, the automatic-update window comes round, or someone asks, and
// never with BUNKER_UPDATE_CHECK=false) and installs one without a shell:
// download, verify the checksum, test-run it, copy the database aside,
// stage it in <data>/bin and restart in place (see internal/selfupdate).

// Release source; tests point these at a fake GitHub.
var (
	UpdateSource = selfupdate.DefaultSource
	// LatestReleaseURL is kept for older tests: when set to a non-GitHub
	// URL it's used as the API base.
	LatestReleaseURL = ""
)

var (
	verMu      sync.Mutex
	verLatest  *selfupdate.Release
	verErr     string
	verChecked time.Time
)

func source() selfupdate.Source {
	s := UpdateSource()
	if LatestReleaseURL != "" {
		s.API = strings.TrimRight(LatestReleaseURL, "/")
	}
	return s
}

func checksOn() bool { return core.EnvBool("UPDATE_CHECK", true) }

// latestRelease returns the newest stable release, cached for maxAge.
func latestRelease(ctx context.Context, maxAge time.Duration) (*selfupdate.Release, string) {
	verMu.Lock()
	defer verMu.Unlock()
	if !verChecked.IsZero() && time.Since(verChecked) < maxAge {
		return verLatest, verErr
	}
	verChecked = time.Now()
	r, err := source().Get(ctx, "latest")
	switch {
	case err == nil:
		verLatest, verErr = r, ""
	case errors.Is(err, selfupdate.ErrNoRelease):
		verLatest, verErr = nil, ""
	default:
		verErr = "Couldn’t reach the release server: " + err.Error()
	}
	return verLatest, verErr
}

// ---- the update job ----

type updateJob struct {
	State    string `json:"state"` // idle, downloading, preparing, restarting, failed
	Version  string `json:"version,omitempty"`
	Done     int64  `json:"done,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started,omitempty"`
	By       string `json:"by,omitempty"`
	Finished int64  `json:"finished,omitempty"`
}

var (
	jobMu   sync.Mutex
	startMu sync.Mutex // one startUpdate at a time
	job     = updateJob{State: "idle"}
	// restartDelay lets the response reach the browser before the restart.
	restartDelay = time.Second
	// requestRestart is replaced in tests.
	requestRestart = core.RequestRestart
)

func jobSnapshot() updateJob {
	jobMu.Lock()
	defer jobMu.Unlock()
	return job
}

func setJob(f func(j *updateJob)) {
	jobMu.Lock()
	f(&job)
	jobMu.Unlock()
}

// cannotUpdate explains why this server can't update itself, or "".
func cannotUpdate() string {
	switch {
	case !selfupdate.Supported():
		return "Updating from this page isn’t available on " + runtime.GOOS + ". On the server, run `bunker update` and restart the Bunker service."
	case !selfupdate.Released(core.Version):
		return "This server runs a development build (" + core.Version + "). Install a release to update from here."
	case app.Setting("update.self") == "false":
		return "Updating from this page is turned off on this server (BUNKER_UPDATE_SELF=false). Update the way Bunker was installed."
	case !checksOn():
		return "Update checks are off on this server (BUNKER_UPDATE_CHECK=false)."
	}
	dir := selfupdate.Dir(app.Cfg.DataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "Bunker can’t write to its data folder (" + err.Error() + ")."
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return "Bunker can’t write to its data folder (" + err.Error() + ")."
	}
	f.Close()
	os.Remove(f.Name())
	return ""
}

// startUpdate begins installing tag ("" = latest) in the background.
func startUpdate(ctx context.Context, tag, by string) (updateJob, error) {
	if why := cannotUpdate(); why != "" {
		return updateJob{}, core.Conflict(why)
	}
	startMu.Lock()
	defer startMu.Unlock()
	if j := jobSnapshot(); j.State == "downloading" || j.State == "preparing" || j.State == "restarting" {
		return j, core.Conflict("An update to " + j.Version + " is already in progress.")
	}
	rel, err := source().Get(ctx, tag)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoRelease) {
			return updateJob{}, core.NewErr(404, "not_found", "No Bunker release has been published yet.")
		}
		return updateJob{}, core.BadRequest("Couldn’t find that release: " + err.Error())
	}
	builtin := selfupdate.Builtin(core.Version)
	switch {
	case rel.Tag == core.Version || strings.TrimPrefix(rel.Tag, "v") == strings.TrimPrefix(core.Version, "v"):
		return updateJob{}, core.Conflict("Bunker " + core.Version + " is already running.")
	case !selfupdate.Newer(rel.Tag, builtin):
		return updateJob{}, core.Conflict(fmt.Sprintf("%s is older than the installed %s. To go back further, reinstall that version the way Bunker was installed.", rel.Tag, builtin))
	}
	if _, _, err := rel.Pick(runtime.GOOS, runtime.GOARCH); err != nil {
		return updateJob{}, core.Conflict(err.Error())
	}
	j := updateJob{State: "downloading", Version: rel.Tag, Started: time.Now().Unix(), By: by}
	setJob(func(x *updateJob) { *x = j })
	actx := core.WithUser(context.WithoutCancel(ctx), core.UserFrom(ctx))
	app.Audit(actx, "admin.update.started", 0, rel.Tag, map[string]any{"from": core.Version, "by": by})
	go runUpdate(actx, rel, by)
	return j, nil
}

func runUpdate(ctx context.Context, rel *selfupdate.Release, by string) {
	fail := func(err error) {
		app.Log.Error("update failed", "version", rel.Tag, "err", err)
		setJob(func(j *updateJob) { j.State, j.Error, j.Finished = "failed", err.Error(), time.Now().Unix() })
		app.Audit(ctx, "admin.update.failed", 0, rel.Tag, map[string]any{"error": err.Error()})
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	bin, err := source().Download(dctx, rel, func(done, total int64) {
		setJob(func(j *updateJob) { j.Done, j.Total = done, total })
	})
	if err != nil {
		fail(err)
		return
	}
	setJob(func(j *updateJob) { j.State = "preparing" })
	if err := backupBeforeUpdate(ctx); err != nil {
		fail(fmt.Errorf("couldn’t copy the database before updating: %w", err))
		return
	}
	if _, err := selfupdate.Stage(app.Cfg.DataDir, rel.Tag, bin, core.Version, by); err != nil {
		fail(err)
		return
	}
	app.Audit(ctx, "admin.update.installed", 0, rel.Tag, map[string]any{"from": core.Version})
	app.Log.Info("update staged; restarting", "from", core.Version, "to", rel.Tag)
	setJob(func(j *updateJob) { j.State = "restarting" })
	time.AfterFunc(restartDelay, requestRestart)
}

func updatesDir() string { return filepath.Join(app.Cfg.DataDir, "updates") }

// backupBeforeUpdate copies the database (VACUUM INTO) to <data>/updates
// and keeps the newest three copies. New versions only add to the schema,
// so the copy is for disasters, not for rolling back.
func backupBeforeUpdate(ctx context.Context) error {
	dir := updatesDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := "before-" + strings.TrimPrefix(core.Version, "v") + "-" + time.Now().UTC().Format("20060102-150405") + ".db"
	if _, err := app.DB.ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(dir, name)); err != nil {
		return err
	}
	ents, _ := os.ReadDir(dir)
	var dbs []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "before-") && strings.HasSuffix(e.Name(), ".db") {
			dbs = append(dbs, e.Name())
		}
	}
	sort.Slice(dbs, func(i, j int) bool { return modTime(dir, dbs[i]).After(modTime(dir, dbs[j])) })
	for i, n := range dbs {
		if i >= 3 {
			os.Remove(filepath.Join(dir, n))
		}
	}
	return nil
}

func modTime(dir, name string) time.Time {
	st, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// ---- automatic updates ----

func autoOn() bool { return app.Setting("update.auto") == "true" }

func autoHour() int {
	if n, err := strconv.Atoi(app.Setting("update.hour")); err == nil && n >= 0 && n < 24 {
		return n
	}
	return 3
}

// autoLoop installs new stable releases during the chosen hour (UTC).
func autoLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			autoTick(ctx, now)
		}
	}
}

func autoTick(ctx context.Context, now time.Time) {
	if !autoOn() || now.UTC().Hour() != autoHour() || cannotUpdate() != "" || jobSnapshot().State != "idle" && jobSnapshot().State != "failed" {
		return
	}
	rel, _ := latestRelease(ctx, time.Hour)
	if rel == nil || rel.Prerelease || !selfupdate.Newer(rel.Tag, core.Version) {
		return
	}
	if f := selfupdate.LastFailure(app.Cfg.DataDir); f != nil && f.Version == rel.Tag {
		return // it already failed to start here; wait for the next release
	}
	if j := jobSnapshot(); j.State == "failed" && j.Version == rel.Tag {
		return
	}
	startUpdate(ctx, rel.Tag, "automatic update")
}

// ---- status ----

func updateStatus(ctx context.Context, maxAge time.Duration) map[string]any {
	builtin := selfupdate.Builtin(core.Version)
	out := map[string]any{
		"current":   core.Version,
		"installed": builtin,
		"staged":    selfupdate.Running(),
		"checks":    checksOn(),
		"platform":  runtime.GOOS + "/" + runtime.GOARCH,
		"job":       jobSnapshot(),
		"auto": map[string]any{
			"enabled": autoOn(), "hour": autoHour(),
			"pinned": app.SettingFromEnv("update.auto") || app.SettingFromEnv("update.hour"),
		},
		"how": "Linux installer: run the install command again (or `bunker update`). Docker: pull the new image and recreate the container. Render, Railway, Fly: redeploy.",
	}
	why := cannotUpdate()
	out["canUpdate"] = why == ""
	if why != "" {
		out["reason"] = why
	}
	if s := selfupdate.ReadState(app.Cfg.DataDir); s != nil && selfupdate.Running() {
		prev := builtin
		if s.Previous != nil && selfupdate.Newer(s.Previous.Version, builtin) {
			prev = s.Previous.Version
		}
		out["rollbackTo"] = prev
		out["installedAt"], out["installedBy"] = s.InstalledAt, s.InstalledBy
	}
	if f := selfupdate.LastFailure(app.Cfg.DataDir); f != nil {
		out["lastFailure"] = f
	}
	if !checksOn() {
		return out
	}
	rel, cerr := latestRelease(ctx, maxAge)
	if cerr != "" {
		out["checkError"] = cerr
	}
	verMu.Lock()
	out["checkedAt"] = verChecked.Unix()
	verMu.Unlock()
	if rel != nil {
		out["latest"] = rel.Tag
		out["release"] = rel
		out["url"] = rel.URL
		out["updateAvailable"] = selfupdate.Released(core.Version) && selfupdate.Newer(rel.Tag, core.Version)
	} else {
		out["updateAvailable"] = false
	}
	return out
}

func registerVersion(a *core.App) {
	who := func(r *http.Request) string {
		if u := core.UserFrom(r.Context()); u != nil {
			return u.Email
		}
		return ""
	}
	a.Handle(core.Op{Name: "admin.version", Method: "GET", Path: "/api/v1/admin/version", Summary: "This server's version and whether a newer release exists", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			s := updateStatus(r.Context(), 12*time.Hour)
			delete(s, "release")
			return s, nil
		}})
	a.Handle(core.Op{Name: "admin.update.status", Method: "GET", Path: "/api/v1/admin/update", Summary: "Update status: versions, the newest release and its notes, a running update, automatic updates", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) { return updateStatus(r.Context(), 12*time.Hour), nil }})
	a.Handle(core.Op{Name: "admin.update.check", Method: "POST", Path: "/api/v1/admin/update/check", Summary: "Check for a new release now", Access: core.AccessAdmin, Body: "none",
		Fn: func(r *http.Request) (any, error) {
			if !checksOn() {
				return nil, core.Conflict("Update checks are off on this server (BUNKER_UPDATE_CHECK=false).")
			}
			return updateStatus(r.Context(), 0), nil
		}})
	a.Handle(core.Op{Name: "admin.update.install", Method: "POST", Path: "/api/v1/admin/update", Summary: "Install a release (the newest by default) and restart Bunker. Sign-ins and data are kept; the restart takes a few seconds", Access: core.AccessAdmin, Destructive: true,
		Params: []core.Param{{Name: "version", In: "body", Type: "string", Desc: "Release tag such as v0.3.0 (default: the newest)"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Version string `json:"version"`
			}
			if r.ContentLength != 0 {
				if err := core.Decode(r, &in); err != nil {
					return nil, err
				}
			}
			j, err := startUpdate(r.Context(), strings.TrimSpace(in.Version), who(r))
			if err != nil {
				return nil, err
			}
			return map[string]any{"job": j}, nil
		}})
	a.Handle(core.Op{Name: "admin.update.rollback", Method: "POST", Path: "/api/v1/admin/update/rollback", Summary: "Go back to the version that ran before the last update and restart Bunker", Access: core.AccessAdmin, Body: "none", Destructive: true,
		Fn: func(r *http.Request) (any, error) {
			if !selfupdate.Running() {
				return nil, core.Conflict("Bunker is running the version it was installed with; there’s nothing to go back to.")
			}
			if j := jobSnapshot(); j.State == "downloading" || j.State == "preparing" || j.State == "restarting" {
				return nil, core.Conflict("Wait for the update to " + j.Version + " to finish.")
			}
			to, err := selfupdate.RollBack(a.Cfg.DataDir, selfupdate.Builtin(core.Version))
			if err != nil {
				return nil, core.Conflict(err.Error())
			}
			a.Audit(r.Context(), "admin.update.rolledback", 0, to, map[string]any{"from": core.Version})
			setJob(func(j *updateJob) {
				*j = updateJob{State: "restarting", Version: to, Started: time.Now().Unix(), By: who(r)}
			})
			time.AfterFunc(restartDelay, requestRestart)
			return map[string]any{"version": to, "job": jobSnapshot()}, nil
		}})
	a.Handle(core.Op{Name: "admin.update.settings", Method: "PUT", Path: "/api/v1/admin/update/settings", Summary: "Turn automatic updates on or off and pick the hour (UTC) they may run", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "auto", In: "body", Type: "boolean", Desc: "Install new stable releases automatically"}, {Name: "hour", In: "body", Type: "integer", Desc: "Hour of the day, 0-23 UTC (default 3)"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct {
				Auto *bool `json:"auto"`
				Hour *int  `json:"hour"`
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			ctx := r.Context()
			if in.Auto != nil {
				if a.SettingFromEnv("update.auto") {
					return nil, core.Conflict("Automatic updates are set by BUNKER_UPDATE_AUTO on this server.")
				}
				if *in.Auto && cannotUpdate() != "" {
					return nil, core.Conflict(cannotUpdate())
				}
				if err := a.SetSetting(ctx, "update.auto", strconv.FormatBool(*in.Auto)); err != nil {
					return nil, err
				}
			}
			if in.Hour != nil {
				if *in.Hour < 0 || *in.Hour > 23 {
					return nil, core.BadRequest("hour must be 0-23 (UTC)")
				}
				if a.SettingFromEnv("update.hour") {
					return nil, core.Conflict("The update hour is set by BUNKER_UPDATE_HOUR on this server.")
				}
				if err := a.SetSetting(ctx, "update.hour", strconv.Itoa(*in.Hour)); err != nil {
					return nil, err
				}
			}
			a.Audit(ctx, "admin.config", 0, "updates", map[string]any{"auto": autoOn(), "hour": autoHour()})
			return map[string]any{"auto": map[string]any{"enabled": autoOn(), "hour": autoHour()}}, nil
		}})
}

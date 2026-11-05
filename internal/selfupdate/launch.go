package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// MaxAttempts is how many starts a staged version gets to become healthy
// before Bunker goes back to the previous one.
const MaxAttempts = 3

// Environment passed from the installed binary to a staged one.
const (
	envLaunched = "BUNKER_SELFUPDATE_LAUNCHED" // set in the staged process
	envEntry    = "BUNKER_SELFUPDATE_ENTRY"    // the installed binary's path
	envBuiltin  = "BUNKER_SELFUPDATE_BUILTIN"  // the installed binary's version
)

// Staged is a release binary kept in <data>/bin.
type Staged struct {
	Version string `json:"version"`
	File    string `json:"file"` // name inside <data>/bin
	SHA256  string `json:"sha256"`
}

// State is <data>/bin/state.json: what to run and what to fall back to.
type State struct {
	Current     Staged  `json:"current"`
	Previous    *Staged `json:"previous,omitempty"` // nil: the installed binary
	Attempts    int     `json:"attempts"`
	Healthy     bool    `json:"healthy"`
	InstalledAt int64   `json:"installedAt"`
	InstalledBy string  `json:"installedBy,omitempty"`
}

// Failure records the last version that couldn't start (<data>/bin/failure.json).
type Failure struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
	At      int64  `json:"at"`
	Running string `json:"running"` // what Bunker went back to
}

// Dir is where staged binaries live.
func Dir(dataDir string) string { return filepath.Join(dataDir, "bin") }

func statePath(dataDir string) string   { return filepath.Join(Dir(dataDir), "state.json") }
func failurePath(dataDir string) string { return filepath.Join(Dir(dataDir), "failure.json") }

// ReadState returns the staged state, or nil when the installed binary runs.
func ReadState(dataDir string) *State {
	b, err := os.ReadFile(statePath(dataDir))
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(b, &s) != nil || s.Current.File == "" {
		return nil
	}
	return &s
}

func writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeState(dataDir string, s *State) error {
	if s == nil {
		err := os.Remove(statePath(dataDir))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeJSON(statePath(dataDir), s)
}

// LastFailure returns the last failed start, if any.
func LastFailure(dataDir string) *Failure {
	b, err := os.ReadFile(failurePath(dataDir))
	if err != nil {
		return nil
	}
	var f Failure
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return &f
}

// ClearFailure forgets the last failed start.
func ClearFailure(dataDir string) { os.Remove(failurePath(dataDir)) }

func recordFailure(dataDir, version, reason, running string) {
	writeJSON(failurePath(dataDir), Failure{Version: version, Reason: reason, At: time.Now().Unix(), Running: running})
}

// Supported reports whether this platform can switch binaries in place.
func Supported() bool { return runtime.GOOS != "windows" }

// Builtin returns the installed binary's version: the one that started
// this process (or this process, when it isn't a staged binary).
func Builtin(self string) string {
	if os.Getenv(envLaunched) == "1" {
		if v := os.Getenv(envBuiltin); v != "" {
			return v
		}
	}
	return self
}

// Running reports whether this process is a staged binary.
func Running() bool { return os.Getenv(envLaunched) == "1" }

// Boot runs first in `bunker serve`. When a newer staged release is
// waiting it replaces this process with it (and doesn't return); otherwise
// it tidies up and returns so the installed binary serves.
func Boot(dataDir, self string, log *slog.Logger) {
	if Running() || !Supported() {
		return
	}
	s := ReadState(dataDir)
	if s == nil {
		return
	}
	if !Newer(s.Current.Version, self) {
		// The installed binary caught up (new image, installer rerun).
		log.Info("installed Bunker is up to date; removing the staged release", "installed", self, "staged", s.Current.Version)
		writeState(dataDir, nil)
		Prune(dataDir, nil)
		return
	}
	if s.Attempts >= MaxAttempts && !s.Healthy {
		fallBack(dataDir, s, self, fmt.Sprintf("it didn't start after %d tries", s.Attempts), log)
		Boot(dataDir, self, log) // the previous staged release, if any
		return
	}
	path := filepath.Join(Dir(dataDir), s.Current.File)
	if sum, err := fileSHA(path); err != nil || !strings.EqualFold(sum, s.Current.SHA256) {
		fallBack(dataDir, s, self, "its file is missing or changed", log)
		Boot(dataDir, self, log)
		return
	}
	s.Attempts++
	s.Healthy = false
	if err := writeState(dataDir, s); err != nil {
		log.Error("can't record the start of the staged release; using the installed one", "err", err)
		return
	}
	entry, _ := os.Executable()
	env := append(withoutSelfEnv(os.Environ()), envLaunched+"=1", envEntry+"="+entry, envBuiltin+"="+self)
	log.Info("starting staged Bunker release", "version", s.Current.Version, "installed", self, "try", s.Attempts)
	err := execve(path, append([]string{path}, os.Args[1:]...), env)
	if err == nil {
		return // only in tests: a real exec doesn't return
	}
	fallBack(dataDir, s, self, "it couldn't be started: "+err.Error(), log)
	Boot(dataDir, self, log)
}

// fallBack gives up on s.Current and switches to s.Previous (or the installed binary).
func fallBack(dataDir string, s *State, self string, reason string, log *slog.Logger) {
	running := self
	var next *State
	if s.Previous != nil && Newer(s.Previous.Version, self) {
		next = &State{Current: *s.Previous, InstalledAt: time.Now().Unix(), Healthy: true}
		running = s.Previous.Version
	}
	log.Error("staged Bunker release failed; going back", "version", s.Current.Version, "reason", reason, "running", running)
	recordFailure(dataDir, s.Current.Version, reason, running)
	writeState(dataDir, next)
	Prune(dataDir, next)
}

func withoutSelfEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, envLaunched+"=") || strings.HasPrefix(kv, envEntry+"=") || strings.HasPrefix(kv, envBuiltin+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// MarkHealthy records that the running staged release started fine.
func MarkHealthy(dataDir, version string) {
	s := ReadState(dataDir)
	if s == nil || s.Current.Version != version || (s.Healthy && s.Attempts == 0) {
		return
	}
	s.Healthy, s.Attempts = true, 0
	writeState(dataDir, s)
	if f := LastFailure(dataDir); f != nil && f.Version == version {
		ClearFailure(dataDir)
	}
}

// Stage writes a verified binary into <data>/bin, checks that it runs and
// reports the expected version, and makes it the one to start next.
// running is the version serving now (it becomes the fallback).
func Stage(dataDir, version string, bin []byte, running string, by string) (*State, error) {
	if !Supported() {
		return nil, errors.New("updating from the admin page isn't available on " + runtime.GOOS + "; run bunker update instead")
	}
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := "bunker-" + safeName(version)
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return nil, fmt.Errorf("can't write to the data folder: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o700); err != nil {
		return nil, err
	}
	if err := checkRuns(tmp.Name(), version); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	s := &State{Current: Staged{Version: version, File: name, SHA256: hex.EncodeToString(sum[:])}, InstalledAt: time.Now().Unix(), InstalledBy: by}
	if old := ReadState(dataDir); old != nil && Running() && old.Current.Version == running && old.Current.Version != version {
		prev := old.Current
		s.Previous = &prev
	}
	if err := writeState(dataDir, s); err != nil {
		return nil, err
	}
	ClearFailure(dataDir)
	Prune(dataDir, s)
	return s, nil
}

// RollBack makes the previous version the one to start next. It returns
// the version that will run.
func RollBack(dataDir, builtin string) (string, error) {
	s := ReadState(dataDir)
	if s == nil {
		return "", errors.New("Bunker is running the version it was installed with; there's nothing to go back to")
	}
	var next *State
	to := builtin
	if s.Previous != nil && Newer(s.Previous.Version, builtin) {
		next = &State{Current: *s.Previous, Healthy: true, InstalledAt: time.Now().Unix()}
		to = s.Previous.Version
	}
	if err := writeState(dataDir, next); err != nil {
		return "", err
	}
	// Automatic updates skip a version someone went back from.
	recordFailure(dataDir, s.Current.Version, "an admin went back to the previous version", to)
	Prune(dataDir, next)
	return to, nil
}

// Prune deletes staged binaries s doesn't mention.
func Prune(dataDir string, s *State) {
	keep := map[string]bool{}
	if s != nil {
		keep[s.Current.File] = true
		if s.Previous != nil {
			keep[s.Previous.File] = true
		}
	}
	ents, _ := os.ReadDir(Dir(dataDir))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "bunker-") && !keep[e.Name()] {
			os.Remove(filepath.Join(Dir(dataDir), e.Name()))
		}
	}
}

// Relaunch replaces this process with the installed binary, which then
// starts whatever Boot picks. It only returns on failure.
func Relaunch() error {
	if !Supported() {
		return errors.New("restarting in place isn't available on " + runtime.GOOS)
	}
	exe := os.Getenv(envEntry)
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	return execve(exe, append([]string{exe}, os.Args[1:]...), withoutSelfEnv(os.Environ()))
}

// checkRuns runs `<bin> version` and expects it to print want.
func checkRuns(path, want string) error {
	out, err := runVersion(path)
	if err != nil {
		return fmt.Errorf("the downloaded build doesn't run on this server: %v", err)
	}
	got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "bunker"))
	if strings.TrimPrefix(got, "v") != strings.TrimPrefix(want, "v") {
		return fmt.Errorf("the downloaded build reports version %q, expected %s", got, want)
	}
	return nil
}

var runVersion = func(path string) (string, error) {
	cmd := exec.Command(path, "version")
	cmd.Env = withoutSelfEnv(os.Environ())
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.Output(); close(done) }()
	select {
	case <-done:
		return string(out), err
	case <-time.After(20 * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", errors.New("timed out")
	}
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func safeName(v string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, v)
}

// execve replaces the process; tests swap it.
var execve = sysExec

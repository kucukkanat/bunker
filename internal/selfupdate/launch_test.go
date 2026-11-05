//go:build !windows

package selfupdate

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin is a script that answers `version` like a Bunker binary.
func fakeBin(v string) []byte { return []byte("#!/bin/sh\necho bunker " + v + "\n") }

type execCall struct {
	path string
	env  []string
}

func stubExec(t *testing.T, fail bool) *[]execCall {
	calls := &[]execCall{}
	old := execve
	execve = func(path string, argv, env []string) error {
		*calls = append(*calls, execCall{path, env})
		if fail {
			return errors.New("exec format error")
		}
		return nil
	}
	t.Cleanup(func() { execve = old })
	t.Setenv(envLaunched, "")
	return calls
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestStageAndBoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := Stage(dir, "v0.3.0", fakeBin("v0.2.9"), "v0.2.0", "ada"); err == nil || !strings.Contains(err.Error(), "reports version") {
		t.Fatalf("wrong version accepted: %v", err)
	}
	s, err := Stage(dir, "v0.3.0", fakeBin("v0.3.0"), "v0.2.0", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if s.Previous != nil || s.Current.File != "bunker-v0.3.0" || s.InstalledBy != "ada" {
		t.Fatalf("state: %+v", s)
	}
	calls := stubExec(t, false)
	Boot(dir, "v0.2.0", quiet)
	if len(*calls) != 1 || (*calls)[0].path != filepath.Join(dir, "bin", "bunker-v0.3.0") {
		t.Fatalf("exec: %+v", *calls)
	}
	env := strings.Join((*calls)[0].env, "\n")
	if !strings.Contains(env, envLaunched+"=1") || !strings.Contains(env, envBuiltin+"=v0.2.0") {
		t.Errorf("env: %s", env)
	}
	if st := ReadState(dir); st.Attempts != 1 || st.Healthy {
		t.Errorf("attempts not counted: %+v", st)
	}
	MarkHealthy(dir, "v0.3.0")
	if st := ReadState(dir); st.Attempts != 0 || !st.Healthy {
		t.Errorf("not healthy: %+v", st)
	}
}

func TestBootFallsBackAfterCrashes(t *testing.T) {
	dir := t.TempDir()
	if _, err := Stage(dir, "v0.3.0", fakeBin("v0.3.0"), "v0.2.0", ""); err != nil {
		t.Fatal(err)
	}
	calls := stubExec(t, false)
	for i := 0; i < MaxAttempts; i++ {
		Boot(dir, "v0.2.0", quiet) // each "start" crashes before MarkHealthy
	}
	Boot(dir, "v0.2.0", quiet)
	if len(*calls) != MaxAttempts {
		t.Fatalf("exec calls = %d, want %d", len(*calls), MaxAttempts)
	}
	if ReadState(dir) != nil {
		t.Error("still staged after crashing")
	}
	f := LastFailure(dir)
	if f == nil || f.Version != "v0.3.0" || f.Running != "v0.2.0" {
		t.Errorf("failure: %+v", f)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "bunker-v0.3.0")); !os.IsNotExist(err) {
		t.Error("failed binary kept")
	}
}

func TestBootFallsBackToPreviousStaged(t *testing.T) {
	dir := t.TempDir()
	if _, err := Stage(dir, "v0.3.0", fakeBin("v0.3.0"), "v0.2.0", ""); err != nil {
		t.Fatal(err)
	}
	// Running v0.3.0 (staged), install v0.4.0: v0.3.0 becomes the fallback.
	t.Setenv(envLaunched, "1")
	s, err := Stage(dir, "v0.4.0", fakeBin("v0.4.0"), "v0.3.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Previous == nil || s.Previous.Version != "v0.3.0" {
		t.Fatalf("previous: %+v", s.Previous)
	}
	calls := stubExec(t, true) // v0.4.0 can't even exec
	Boot(dir, "v0.2.0", quiet)
	if len(*calls) != 2 || !strings.HasSuffix((*calls)[1].path, "bunker-v0.3.0") {
		t.Fatalf("exec: %+v", *calls)
	}
}

func TestBootTamperedAndOutdated(t *testing.T) {
	dir := t.TempDir()
	if _, err := Stage(dir, "v0.3.0", fakeBin("v0.3.0"), "v0.2.0", ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "bin", "bunker-v0.3.0"), []byte("#!/bin/sh\necho evil\n"), 0o700)
	calls := stubExec(t, false)
	Boot(dir, "v0.2.0", quiet)
	if len(*calls) != 0 || ReadState(dir) != nil {
		t.Fatalf("ran a changed binary: %+v", *calls)
	}

	// The installed binary catches up: the staged one is dropped.
	if _, err := Stage(dir, "v0.3.0", fakeBin("v0.3.0"), "v0.2.0", ""); err != nil {
		t.Fatal(err)
	}
	Boot(dir, "v0.3.0", quiet)
	if len(*calls) != 0 || ReadState(dir) != nil {
		t.Fatal("staged release kept although the installed one is as new")
	}
}

func TestRollBack(t *testing.T) {
	dir := t.TempDir()
	if _, err := RollBack(dir, "v0.2.0"); err == nil {
		t.Fatal("rolled back with nothing staged")
	}
	Stage(dir, "v0.3.0", fakeBin("v0.3.0"), "v0.2.0", "")
	t.Setenv(envLaunched, "1")
	Stage(dir, "v0.4.0", fakeBin("v0.4.0"), "v0.3.0", "")
	to, err := RollBack(dir, "v0.2.0")
	if err != nil || to != "v0.3.0" || ReadState(dir).Current.Version != "v0.3.0" {
		t.Fatalf("rollback to %s: %v", to, err)
	}
	if f := LastFailure(dir); f == nil || f.Version != "v0.4.0" {
		t.Errorf("rolled-back version not remembered: %+v", f)
	}
	if to, _ := RollBack(dir, "v0.2.0"); to != "v0.2.0" || ReadState(dir) != nil {
		t.Fatalf("second rollback to %s", to)
	}
}

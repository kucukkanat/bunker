package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A project (a downloaded template, a cloned repo, an AI-generated build)
// can contain a symlink like dist/logo.png -> ~/.ssh/id_ed25519. Deploying
// it must not publish files from outside the folder.
func TestDeploySkipsSymlinksLeavingTheFolder(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_ed25519")
	os.WriteFile(secret, []byte("-----BEGIN OPENSSH PRIVATE KEY-----"), 0o600)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644)
	os.WriteFile(filepath.Join(dir, "real.css"), []byte("body{}"), 0o644)
	if err := os.Symlink(secret, filepath.Join(dir, "logo.png")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	os.Symlink(filepath.Join(dir, "real.css"), filepath.Join(dir, "alias.css")) // inside: fine
	files, err := collectFiles(dir, NewIgnorer(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Rel] = true
	}
	if got["logo.png"] {
		t.Fatal("a symlink to a file outside the project was uploaded")
	}
	if !got["index.html"] || !got["alias.css"] {
		t.Fatalf("files: %v", got)
	}
}

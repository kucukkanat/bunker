package core

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Blobs is a content-addressed file store. Identical files across
// versions and sites are stored once, which keeps disk usage low and
// makes deploys of unchanged files free.
type Blobs struct{ Dir string }

func NewBlobs(dir string) (*Blobs, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, err
	}
	return &Blobs{Dir: dir}, nil
}

func (b *Blobs) path(hash string) string {
	return filepath.Join(b.Dir, hash[:2], hash[2:])
}

// Put stores r and returns its sha256 hex and size. If max > 0 and the
// content exceeds it, TooLarge is returned.
func (b *Blobs) Put(r io.Reader, max int64) (string, int64, error) {
	f, err := os.CreateTemp(filepath.Join(b.Dir, "tmp"), "up-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	var src io.Reader = r
	if max > 0 {
		src = io.LimitReader(r, max+1)
	}
	n, err := io.Copy(io.MultiWriter(f, h), src)
	if err != nil {
		f.Close()
		return "", 0, err
	}
	if max > 0 && n > max {
		f.Close()
		return "", 0, TooLarge("file is larger than the allowed size")
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	dst := b.path(sum)
	if _, err := os.Stat(dst); err == nil {
		return sum, n, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, err
	}
	if err := os.Rename(f.Name(), dst); err != nil {
		return "", 0, err
	}
	return sum, n, nil
}

// PutBytes stores data.
func (b *Blobs) PutBytes(data []byte) (string, int64, error) {
	return b.Put(strings.NewReader(string(data)), 0)
}

// Open opens a blob for reading.
func (b *Blobs) Open(hash string) (*os.File, error) {
	if len(hash) != 64 {
		return nil, errors.New("bad hash")
	}
	return os.Open(b.path(hash))
}

// Read returns the full blob.
func (b *Blobs) Read(hash string) ([]byte, error) {
	if len(hash) != 64 {
		return nil, errors.New("bad hash")
	}
	return os.ReadFile(b.path(hash))
}

// Exists reports whether a blob is present.
func (b *Blobs) Exists(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := os.Stat(b.path(hash))
	return err == nil
}

// Gzip returns the path of a cached gzip variant, creating it lazily.
// Returns "" if compression isn't worthwhile.
func (b *Blobs) Gzip(hash string) string {
	if len(hash) != 64 {
		return ""
	}
	gz := b.path(hash) + ".gz"
	if _, err := os.Stat(gz); err == nil {
		return gz
	}
	src, err := os.Open(b.path(hash))
	if err != nil {
		return ""
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Join(b.Dir, "tmp"), "gz-*")
	if err != nil {
		return ""
	}
	zw, _ := gzip.NewWriterLevel(tmp, gzip.BestCompression)
	if _, err := io.Copy(zw, src); err != nil {
		zw.Close()
		tmp.Close()
		os.Remove(tmp.Name())
		return ""
	}
	zw.Close()
	tmp.Close()
	if err := os.Rename(tmp.Name(), gz); err != nil {
		os.Remove(tmp.Name())
		return ""
	}
	return gz
}

// Remove deletes a blob (and its compressed variants).
func (b *Blobs) Remove(hash string) {
	if len(hash) != 64 {
		return
	}
	os.Remove(b.path(hash))
	os.Remove(b.path(hash) + ".gz")
}

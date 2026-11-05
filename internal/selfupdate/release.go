// Package selfupdate finds Bunker releases, verifies and stages their
// binaries in the data folder, and starts the newest good one on boot.
//
// Release binaries never overwrite the installed one (it's often
// read-only: a container image or /usr/local/bin under systemd). They go in
// <data>/bin and the installed binary hands over to them on start, unless it
// is itself newer, so pulling a newer image or rerunning the installer
// still wins.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// Release is one published Bunker release.
type Release struct {
	Tag        string  `json:"tag"`
	Name       string  `json:"name,omitempty"`
	URL        string  `json:"url,omitempty"`
	Notes      string  `json:"notes,omitempty"`
	Published  int64   `json:"published,omitempty"`
	Prerelease bool    `json:"prerelease,omitempty"`
	Assets     []Asset `json:"-"`
}

// Asset is a downloadable file of a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// ErrNoRelease means the repository has no published release.
var ErrNoRelease = errors.New("no Bunker release has been published yet")

// Source says where releases come from (BUNKER_UPDATE_REPO, BUNKER_UPDATE_API).
type Source struct {
	Repo   string // owner/name
	API    string // GitHub API base
	Client *http.Client
	Agent  string
}

// DefaultSource reads BUNKER_UPDATE_REPO and BUNKER_UPDATE_API.
func DefaultSource() Source {
	repo := os.Getenv("BUNKER_UPDATE_REPO")
	if repo == "" {
		repo = "kucukkanat/bunker"
	}
	api := strings.TrimRight(os.Getenv("BUNKER_UPDATE_API"), "/")
	if api == "" {
		api = "https://api.github.com"
	}
	return Source{Repo: repo, API: api, Client: &http.Client{Timeout: 10 * time.Minute}, Agent: "bunker-update"}
}

// Get fetches a release: tag "" or "latest" means the newest stable one.
func (s Source) Get(ctx context.Context, tag string) (*Release, error) {
	path := "/repos/" + s.Repo + "/releases/latest"
	if tag != "" && tag != "latest" {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		path = "/repos/" + s.Repo + "/releases/tags/" + tag
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.API+path, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", s.Agent)
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		if path != "/repos/"+s.Repo+"/releases/latest" {
			return nil, fmt.Errorf("there's no release %s", tag)
		}
		return nil, ErrNoRelease
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("release lookup failed: %s", res.Status)
	}
	var body struct {
		Tag        string `json:"tag_name"`
		Name       string `json:"name"`
		URL        string `json:"html_url"`
		Body       string `json:"body"`
		Published  string `json:"published_at"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("release lookup: %w", err)
	}
	if body.Draft || body.Tag == "" {
		return nil, ErrNoRelease
	}
	r := &Release{Tag: body.Tag, Name: body.Name, URL: body.URL, Notes: body.Body, Prerelease: body.Prerelease}
	if len(r.Notes) > 20000 {
		r.Notes = r.Notes[:20000] + "…"
	}
	if t, err := time.Parse(time.RFC3339, body.Published); err == nil {
		r.Published = t.Unix()
	}
	for _, a := range body.Assets {
		r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return r, nil
}

func (s Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// Pick returns the build for goos/goarch and the checksum file to check it.
func (r *Release) Pick(goos, goarch string) (bin Asset, sums Asset, err error) {
	archs := []string{goarch}
	switch goarch {
	case "amd64":
		archs = append(archs, "x86_64", "x64")
	case "arm64":
		archs = append(archs, "aarch64")
	}
	osNames := []string{goos}
	if goos == "darwin" {
		osNames = append(osNames, "macos", "mac")
	}
	for _, a := range r.Assets {
		n := strings.ToLower(a.Name)
		if strings.Contains(n, "checksum") || strings.Contains(n, "sha256sums") {
			sums = a
			continue
		}
		if strings.HasSuffix(n, ".sha256") || strings.HasSuffix(n, ".sig") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".sbom") || strings.HasSuffix(n, ".json") {
			continue
		}
		if containsAny(n, osNames) && containsAny(n, archs) && bin.URL == "" {
			bin = a
		}
	}
	if bin.URL == "" {
		return bin, sums, fmt.Errorf("release %s has no build for %s/%s", r.Tag, goos, goarch)
	}
	for _, a := range r.Assets {
		if a.Name == bin.Name+".sha256" {
			sums = a
		}
	}
	if sums.URL == "" {
		return bin, sums, fmt.Errorf("release %s has no checksums; not installing an unverified binary", r.Tag)
	}
	return bin, sums, nil
}

// Download fetches this platform's binary, checks its SHA-256 against the
// release checksums and returns the executable's bytes. progress, when set,
// is called with bytes received so far and the expected total.
func (s Source) Download(ctx context.Context, r *Release, progress func(done, total int64)) ([]byte, error) {
	bin, sums, err := r.Pick(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	data, err := s.fetch(ctx, bin.URL, 300<<20, bin.Size, progress)
	if err != nil {
		return nil, err
	}
	sumData, err := s.fetch(ctx, sums.URL, 1<<20, 0, nil)
	if err != nil {
		return nil, err
	}
	want := FindChecksum(string(sumData), bin.Name)
	got := sha256.Sum256(data)
	if want == "" || !strings.EqualFold(want, hex.EncodeToString(got[:])) {
		return nil, fmt.Errorf("checksum mismatch for %s; not installing", bin.Name)
	}
	return ExtractBinary(bin.Name, data)
}

func (s Source) fetch(ctx context.Context, u string, limit, total int64, progress func(done, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.Agent)
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("download failed: %s", res.Status)
	}
	if total <= 0 {
		total = res.ContentLength
	}
	var buf bytes.Buffer
	chunk := make([]byte, 64<<10)
	body := io.LimitReader(res.Body, limit+1)
	for {
		n, err := body.Read(chunk)
		buf.Write(chunk[:n])
		if progress != nil && n > 0 {
			progress(int64(buf.Len()), total)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if int64(buf.Len()) > limit {
		return nil, fmt.Errorf("download is larger than %d MB", limit>>20)
	}
	return buf.Bytes(), nil
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// FindChecksum reads "sha256  name" lines (or a bare hash).
func FindChecksum(text, name string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	var only string
	n := 0
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		n++
		only = f[0]
		if len(f) >= 2 && strings.TrimPrefix(f[len(f)-1], "*") == name {
			return f[0]
		}
	}
	if n == 1 && len(only) == 64 {
		return only
	}
	return ""
}

// ExtractBinary returns the bunker executable from a raw, .tar.gz or .zip asset.
func ExtractBinary(name string, data []byte) ([]byte, error) {
	want := "bunker"
	if runtime.GOOS == "windows" {
		want = "bunker.exe"
	}
	base := func(p string) string { return p[strings.LastIndexAny(p, `/\`)+1:] }
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err != nil {
				return nil, fmt.Errorf("no %s inside %s", want, name)
			}
			if base(h.Name) == want && h.Typeflag == tar.TypeReg {
				return io.ReadAll(io.LimitReader(tr, 300<<20))
			}
		}
	case strings.HasSuffix(n, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 300<<20))
			}
		}
		return nil, fmt.Errorf("no %s inside %s", want, name)
	}
	return data, nil
}

// Newer reports whether version a is newer than b (vX.Y.Z; a pre-release
// such as v1.2.0-rc.1 is older than v1.2.0).
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return !isPre(a) && isPre(b)
}

func isPre(v string) bool { return strings.Contains(v, "-") }

func parts(v string) [3]int {
	v = strings.TrimPrefix(strings.SplitN(v, "-", 2)[0], "v")
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

// Released reports whether v looks like a release version (not dev/source).
func Released(v string) bool {
	v = strings.TrimPrefix(v, "v")
	return v != "" && v[0] >= '0' && v[0] <= '9' && strings.Count(strings.SplitN(v, "-", 2)[0], ".") == 2
}

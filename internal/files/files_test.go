package files_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/server"

	_ "github.com/kucukkanat/bunker/internal/auth"
	_ "github.com/kucukkanat/bunker/internal/data"
	_ "github.com/kucukkanat/bunker/internal/files"
	_ "github.com/kucukkanat/bunker/internal/sites"
)

var (
	srv *httptest.Server
	app *core.App
)

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "bunker-files-test")
	os.Setenv("BUNKER_ADMINS", "nobody@x.test")
	os.Setenv("BUNKER_UPLOAD_MAX_MB", "1")
	cfg := &core.Config{Dev: true, DataDir: dir}
	a, err := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		panic(err)
	}
	app = a
	srv = httptest.NewServer(server.Handler(a))
	a.Cfg.URL = srv.URL
	code := m.Run()
	srv.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

func client(t *testing.T, email string) *http.Client {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if email != "" {
		res, err := hc.Get(srv.URL + "/auth/dev?as=" + url.QueryEscape(email) + "&name=" + url.QueryEscape(strings.Split(email, "@")[0]))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	return hc
}

func call(t *testing.T, hc *http.Client, method, path string, body io.Reader, ct string) (int, []byte, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, body)
	req.Header.Set("X-Bunker", "1")
	req.Header.Set("X-Bunker-Page", "1")
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	res, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

func uploadBody(name string, content []byte, folder string) (io.Reader, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if folder != "" {
		mw.WriteField("folder", folder)
	}
	w, _ := mw.CreateFormFile("file", name)
	w.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

type file struct {
	ID, Name, Folder, Type, URL string
	Size                        int64
	Author                      int64 `json:"_author"`
}

func setup(t *testing.T) (string, *http.Client, *http.Client, *http.Client) {
	adm := &http.Client{}
	slug := "files-" + strings.ToLower(core.RandomToken(4))
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return 'x'
	}, slug)
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/sites", strings.NewReader(`{"title":"Files","slug":"`+slug+`"}`))
	req.Header.Set("X-Bunker", "1")
	res, err := adm.Do(req)
	if err != nil || res.StatusCode != 201 {
		t.Fatalf("create: %v %v", err, res.Status)
	}
	res.Body.Close()
	for _, e := range []string{"ann@x.test", "bob@x.test"} {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/sites/"+slug+"/members", strings.NewReader(`{"type":"user","id":"`+e+`","role":"viewer"}`))
		req.Header.Set("X-Bunker", "1")
		res, _ := adm.Do(req)
		res.Body.Close()
	}
	return slug, adm, client(t, "ann@x.test"), client(t, "bob@x.test")
}

func TestUploadServeDelete(t *testing.T) {
	slug, adm, ann, bob := setup(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1, 2, 3, 4}, 300)...)
	body, ct := uploadBody("../../evil\"name.png", png, "receipts/2026")
	st, b, _ := call(t, ann, "POST", "/api/v1/sites/"+slug+"/uploads", body, ct)
	if st != 201 {
		t.Fatalf("upload: %d %s", st, b)
	}
	var f file
	json.Unmarshal(b, &f)
	if f.Name != "evil_name.png" || f.Folder != "receipts/2026" || f.Type != "image/png" || f.Size != int64(len(png)) || !strings.HasPrefix(f.URL, "/s/"+slug+"/~f/") {
		t.Fatalf("file: %+v", f)
	}
	// serve with the viewer's ticket (no cookie needed, like a sandboxed <img>)
	res, err := http.Get(srv.URL + f.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(got, png) {
		t.Fatalf("serve: %d", res.StatusCode)
	}
	h := res.Header
	if h.Get("Content-Type") != "image/png" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") || !strings.Contains(h.Get("Cache-Control"), "immutable") || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers: %v", h)
	}
	// range
	req, _ := http.NewRequest("GET", srv.URL+f.URL, nil)
	req.Header.Set("Range", "bytes=0-7")
	res, _ = http.DefaultClient.Do(req)
	part, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || string(part) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("range: %d %q", res.StatusCode, part)
	}
	// tampered ticket
	bad := strings.Replace(f.URL, "/~f/", "/~f/9", 1)
	res, _ = http.Get(srv.URL + bad)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("bad ticket: %d", res.StatusCode)
	}
	// list
	st, b, _ = call(t, bob, "GET", "/api/v1/sites/"+slug+"/uploads?folder=receipts/2026", nil, "")
	var list struct{ Items []file }
	json.Unmarshal(b, &list)
	if st != 200 || len(list.Items) != 1 {
		t.Fatalf("list: %d %s", st, b)
	}
	// content via the API
	st, b, _ = call(t, bob, "GET", "/api/v1/sites/"+slug+"/uploads/"+f.ID+"/content", nil, "")
	if st != 200 || !bytes.Equal(b, png) {
		t.Fatalf("content: %d", st)
	}
	// own rule: bob can't delete ann's file
	if st, _, _ := call(t, bob, "DELETE", "/api/v1/sites/"+slug+"/uploads/"+f.ID, nil, ""); st != 403 {
		t.Fatalf("bob delete: %d", st)
	}
	// private rule (editor-only op, not page-safe)
	req, _ = http.NewRequest("PUT", srv.URL+"/api/v1/sites/"+slug+"/uploads/_rule", strings.NewReader(`{"rule":"private"}`))
	req.Header.Set("X-Bunker", "1")
	res, _ = adm.Do(req)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("rule: %d", res.StatusCode)
	}
	st, b, _ = call(t, bob, "GET", "/api/v1/sites/"+slug+"/uploads", nil, "")
	json.Unmarshal(b, &list)
	if len(list.Items) != 0 {
		t.Fatalf("private list leaked: %s", b)
	}
	// bob's own ticket can't fetch ann's private file
	st, b, _ = call(t, bob, "GET", "/api/v1/sites/"+slug+"/uploads/"+f.ID, nil, "")
	if st != 404 {
		t.Fatalf("private get: %d", st)
	}
	if st, _, _ := call(t, ann, "DELETE", "/api/v1/sites/"+slug+"/uploads/"+f.ID, nil, ""); st != 204 {
		t.Fatalf("ann delete: %d", st)
	}
	res, _ = http.Get(srv.URL + f.URL)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("deleted still served: %d", res.StatusCode)
	}
}

func TestBlockedAndLimits(t *testing.T) {
	slug, _, ann, _ := setup(t)
	cases := []struct {
		name    string
		content []byte
		want    int
	}{
		{"setup.exe", []byte("MZ..."), 415},
		{"page.html", []byte("<script>"), 415},
		{"noext", []byte("hi"), 415},
		{"photo.png", []byte("MZ\x90\x00 fake png"), 415}, // sniffed executable
		{"run.txt", []byte("#!/bin/sh\nrm -rf /"), 415},
		{"big.pdf", bytes.Repeat([]byte("x"), 2<<20), 413},
		{"notes.txt", []byte("hello"), 201},
	}
	for _, c := range cases {
		body, ct := uploadBody(c.name, c.content, "")
		st, b, _ := call(t, ann, "POST", "/api/v1/sites/"+slug+"/uploads", body, ct)
		if st != c.want {
			t.Errorf("%s: %d, want %d: %s", c.name, st, c.want, b)
		}
		if st == 201 {
			var f file
			json.Unmarshal(b, &f)
			res, _ := http.Get(srv.URL + f.URL)
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") || res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Errorf("text served as %v", res.Header)
			}
		}
	}
}

func TestGCHook(t *testing.T) {
	slug, _, ann, _ := setup(t)
	body, ct := uploadBody("a.csv", []byte("a,b\n1,2\n"), "")
	_, b, _ := call(t, ann, "POST", "/api/v1/sites/"+slug+"/uploads", body, ct)
	var f file
	json.Unmarshal(b, &f)
	var hash string
	app.DB.QueryRow(`SELECT hash FROM uploads WHERE id=?`, f.ID).Scan(&hash)
	used := map[string]bool{}
	app.Emit(context.Background(), "blobs.gc", used)
	if !used[hash] {
		t.Fatal("upload hash not reported to blobs.gc")
	}
}

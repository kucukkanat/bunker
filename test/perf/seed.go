package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

type seedData struct {
	cookies  []string // session cookie per user (index = user number)
	sites    []string // slugs, all visible to viewers
	viewers  []int    // users that see every site but own none
	dataSite string
	docIDs   []string
	// tickets[v][s]: content base "/s/<slug>/~r/<ticket>/" for viewers[v], sites[s]
	tickets [][]string
}

const (
	owners     = 10 // users 0..9 own the sites
	viewerFrom = 10 // users 10.. are viewers
	numViewers = 20 // users 10..29 are measured as viewers
	group      = "perf-team"
)

var httpc = newClient(64)

// call makes a request and decodes a JSON response into out (if given).
func (h *harness) call(method, path, cookie string, body io.Reader, ctype string, out any) (int, error) {
	req, _ := http.NewRequest(method, h.base+path, body)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if method != "GET" {
		req.Header.Set("X-Bunker", "1")
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := httpc.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, truncate(string(b), 300))
	}
	if out != nil {
		return res.StatusCode, json.Unmarshal(b, out)
	}
	return res.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func jsonBody(v any) io.Reader { b, _ := json.Marshal(v); return bytes.NewReader(b) }

func (h *harness) seedAll(nUsers, nSites, nDocs int) *seedData {
	sd := &seedData{cookies: make([]string, nUsers)}
	// Users: /auth/dev?as=... creates the account and a session.
	parallel(nUsers, 16, func(i int) {
		q := url.Values{"as": {fmt.Sprintf("u%d@perf.test", i)}, "name": {fmt.Sprintf("User %d", i)}, "groups": {group}}
		req, _ := http.NewRequest("GET", h.base+"/auth/dev?"+q.Encode(), nil)
		res, err := httpc.Do(req)
		check(err)
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		for _, c := range res.Cookies() {
			if c.Name == "bunker_session" {
				sd.cookies[i] = c.Name + "=" + c.Value
			}
		}
		if sd.cookies[i] == "" {
			check(fmt.Errorf("no session cookie for user %d (status %d)", i, res.StatusCode))
		}
	})
	for i := 0; i < numViewers && viewerFrom+i < nUsers; i++ {
		sd.viewers = append(sd.viewers, viewerFrom+i)
	}

	// Sites: dropped by owners 0..9; a third org-visible, a third shared
	// with the group, a third shared with each viewer by email.
	sd.sites = make([]string, nSites)
	files := siteFiles()
	parallel(nSites, 8, func(j int) {
		var out struct{ Slug string }
		body, ct := multipartFiles(map[string]string{"title": fmt.Sprintf("Perf site %d", j), "slug": fmt.Sprintf("perf-%d", j)}, files)
		_, err := h.call("POST", "/api/v1/drop", sd.cookies[j%owners], body, ct, &out)
		check(err)
		sd.sites[j] = out.Slug
		owner := sd.cookies[j%owners]
		switch j % 3 {
		case 0:
			_, err = h.call("PATCH", "/api/v1/sites/"+out.Slug, owner, jsonBody(map[string]any{"orgView": true}), "application/json", nil)
		case 1:
			_, err = h.call("POST", "/api/v1/sites/"+out.Slug+"/members", owner, jsonBody(map[string]any{"type": "group", "id": group, "role": "viewer"}), "application/json", nil)
		case 2:
			for _, v := range sd.viewers {
				if _, err = h.call("POST", "/api/v1/sites/"+out.Slug+"/members", owner, jsonBody(map[string]any{"type": "user", "id": fmt.Sprintf("u%d@perf.test", v), "role": "viewer"}), "application/json", nil); err != nil {
					break
				}
			}
		}
		check(err)
	})

	// Data site: one collection with nDocs documents, readable by everyone
	// in the org ("own" rule: read all, change your own).
	sd.dataSite = "perf-data"
	body, ct := multipartFiles(map[string]string{"title": "Perf data", "slug": sd.dataSite}, map[string][]byte{"index.html": []byte("<!doctype html><html><head><title>data</title></head><body>data</body></html>")})
	_, err := h.call("POST", "/api/v1/drop", sd.cookies[0], body, ct, nil)
	check(err)
	_, err = h.call("PATCH", "/api/v1/sites/"+sd.dataSite, sd.cookies[0], jsonBody(map[string]any{"orgView": true}), "application/json", nil)
	check(err)
	statuses := []string{"open", "closed", "waiting", "done"}
	for start := 0; start < nDocs; start += 2000 {
		var arr []map[string]any
		for i := start; i < min(nDocs, start+2000); i++ {
			arr = append(arr, map[string]any{"n": i, "status": statuses[i%4], "title": fmt.Sprintf("Item number %d", i),
				"amount": float64(i%997) * 1.25, "tags": []string{"a", "b"}, "note": strings.Repeat("x", 80)})
		}
		_, err = h.call("POST", "/api/v1/sites/"+sd.dataSite+"/db/items/_import", sd.cookies[0], jsonBody(arr), "application/json", nil)
		check(err)
	}
	var list struct {
		Items []struct {
			ID string `json:"_id"`
		}
	}
	_, err = h.call("GET", "/api/v1/sites/"+sd.dataSite+"/db/items?limit=500", sd.cookies[viewerFrom], nil, "", &list)
	check(err)
	for _, it := range list.Items {
		sd.docIDs = append(sd.docIDs, it.ID)
	}

	// Tickets: open each site's shell page once per viewer.
	baseRe := regexp.MustCompile(`data-base="([^"]+)"`)
	sd.tickets = make([][]string, len(sd.viewers))
	var mu sync.Mutex
	for v := range sd.viewers {
		sd.tickets[v] = make([]string, nSites)
	}
	parallel(len(sd.viewers)*nSites, 16, func(k int) {
		v, s := k/nSites, k%nSites
		req, _ := http.NewRequest("GET", h.base+"/s/"+sd.sites[s]+"/", nil)
		req.Header.Set("Cookie", sd.cookies[sd.viewers[v]])
		res, err := httpc.Do(req)
		check(err)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		m := baseRe.FindSubmatch(b)
		if m == nil {
			check(fmt.Errorf("no content base for viewer %d site %s (status %d)", v, sd.sites[s], res.StatusCode))
		}
		mu.Lock()
		sd.tickets[v][s] = string(m[1])
		mu.Unlock()
	})
	return sd
}

func parallel(n, workers int, f func(i int)) {
	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				f(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
}

// siteFiles is a small but realistic static site: HTML with root-relative
// links (exercises SDK injection + path rewriting), CSS, JS and an image.
func siteFiles() map[string][]byte {
	var html strings.Builder
	html.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Perf</title>
<link rel="stylesheet" href="/app.css"><script src="/app.js" defer></script></head><body><header><img src="/logo.png" alt=""><nav>`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&html, `<a href="/page%d.html">Page %d</a> `, i, i)
	}
	html.WriteString(`</nav></header><main>`)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&html, "<p>Paragraph %d of the perf page with some text so it compresses like a real page would. Lorem ipsum dolor sit amet.</p>\n", i)
	}
	html.WriteString(`</main></body></html>`)
	var css strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&css, ".c%d{color:#1f3a2e;margin:%dpx;padding:4px 8px;background:url(/logo.png) no-repeat}\n", i, i)
	}
	var js strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&js, "function f%d(a,b){return a*%d+b-Math.max(a,b)/%d}\n", i, i, i+1)
	}
	img := make([]byte, 12<<10)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img {
		img[i] = byte(r.UintN(256))
	}
	copy(img, "\x89PNG\r\n\x1a\n")
	return map[string][]byte{"index.html": []byte(html.String()), "app.css": []byte(css.String()), "app.js": []byte(js.String()), "logo.png": img}
}

func multipartFiles(fields map[string]string, files map[string][]byte) (io.Reader, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	for name, data := range files {
		fw, _ := mw.CreateFormFile("file:"+name, name)
		fw.Write(data)
	}
	mw.Close()
	return &b, mw.FormDataContentType()
}

// zip5MB builds a ~5 MB zip: a few hundred text files and some binary assets.
func zip5MB(seed uint64) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	r := rand.New(rand.NewPCG(seed, 7))
	f, _ := zw.Create("index.html")
	f.Write([]byte(`<!doctype html><html><head><title>Upload</title><link rel="stylesheet" href="/s.css"></head><body><img src="/img/0.jpg"></body></html>`))
	for i := 0; i < 200; i++ {
		f, _ := zw.Create(fmt.Sprintf("docs/page%d.html", i))
		for k := 0; k < 60; k++ {
			fmt.Fprintf(f, "<p>Page %d paragraph %d with text %d.</p>\n", i, k, r.UintN(1e9))
		}
	}
	for i := 0; i < 16; i++ {
		f, _ := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("img/%d.jpg", i), Method: zip.Store})
		buf := make([]byte, 280<<10)
		for k := range buf {
			buf[k] = byte(r.UintN(256))
		}
		f.Write(buf)
	}
	zw.Close()
	return b.Bytes()
}

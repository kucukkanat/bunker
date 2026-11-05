package sites

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/kucukkanat/bunker/internal/core"
)

// FileRef is one file in a manifest.
type FileRef struct {
	Hash string `json:"h"`
	Size int64  `json:"s"`
	Type string `json:"t"`
}

// Manifest maps clean relative paths ("index.html", "img/a.png") to blobs.
type Manifest map[string]FileRef

// Size returns the total byte size.
func (m Manifest) Size() int64 {
	var n int64
	for _, f := range m {
		n += f.Size
	}
	return n
}

// Paths returns sorted paths.
func (m Manifest) Paths() []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

var (
	mcMu   sync.Mutex
	mCache = map[string]Manifest{}
)

// LoadManifest returns the manifest of version n (0 = draft). Versions are
// immutable so they're cached forever (bounded).
func LoadManifest(ctx context.Context, siteID, n int64) (Manifest, error) {
	key := itoa64(siteID) + "@" + itoa64(n)
	if n > 0 {
		mcMu.Lock()
		if m, ok := mCache[key]; ok {
			mcMu.Unlock()
			return m, nil
		}
		mcMu.Unlock()
	}
	var raw string
	var err error
	if n == 0 {
		err = app.DB.QueryRowContext(ctx, `SELECT manifest FROM drafts WHERE site_id=?`, siteID).Scan(&raw)
	} else {
		err = app.DB.QueryRowContext(ctx, `SELECT manifest FROM versions WHERE site_id=? AND n=?`, siteID, n).Scan(&raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.NotFound("version not found")
	}
	if err != nil {
		return nil, err
	}
	m := Manifest{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	if n > 0 {
		mcMu.Lock()
		if len(mCache) > 5000 {
			mCache = map[string]Manifest{}
		}
		mCache[key] = m
		mcMu.Unlock()
	}
	return m, nil
}

// Transformer converts dropped files into a servable site (e.g. Markdown
// to HTML, CSV to a table app). It may modify files in place and returns
// the detected kind ("" if not applicable).
type Transformer struct {
	Name     string
	Priority int
	Apply    func(ctx context.Context, files map[string][]byte) (kind string, err error)
}

var (
	trMu         sync.Mutex
	transformers []Transformer
)

// RegisterTransformer adds a deploy-time transformer.
func RegisterTransformer(t Transformer) {
	trMu.Lock()
	defer trMu.Unlock()
	transformers = append(transformers, t)
	sort.SliceStable(transformers, func(i, j int) bool { return transformers[i].Priority < transformers[j].Priority })
}

type warnKey struct{}

type warnings struct {
	mu   sync.Mutex
	list []string
}

// WithWarnings returns a context that collects deploy warnings.
func WithWarnings(ctx context.Context) (context.Context, func() []string) {
	w := &warnings{}
	return context.WithValue(ctx, warnKey{}, w), func() []string {
		w.mu.Lock()
		defer w.mu.Unlock()
		return append([]string(nil), w.list...)
	}
}

// AddWarning records a human-readable deploy warning (shown after upload).
func AddWarning(ctx context.Context, msg string) {
	if w, ok := ctx.Value(warnKey{}).(*warnings); ok {
		w.mu.Lock()
		if len(w.list) < 20 {
			w.list = append(w.list, msg)
		}
		w.mu.Unlock()
	}
}

// CleanPath normalizes and validates a site file path.
func CleanPath(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return "", core.BadRequest("empty path")
	}
	c := path.Clean(p)
	if c == "." || strings.HasPrefix(c, "../") || c == ".." || strings.Contains(c, "\x00") {
		return "", core.BadRequest("invalid path " + p)
	}
	if len(c) > 400 {
		return "", core.BadRequest("path too long")
	}
	return c, nil
}

// skipFile drops OS junk and VCS folders from uploads.
func skipFile(p string) bool {
	base := path.Base(p)
	if base == ".DS_Store" || base == "Thumbs.db" || base == "desktop.ini" || strings.HasPrefix(base, "._") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".git" || seg == "node_modules" || seg == "__MACOSX" || seg == ".svn" || seg == ".hg" || seg == ".idea" || seg == ".vscode" {
			return true
		}
	}
	return false
}

// Normalize prepares dropped files: strips a single wrapping folder, picks
// a build output folder (dist/build/out/public) when the upload is a
// project, runs transformers, and ensures there's an entry page.
func Normalize(ctx context.Context, files map[string][]byte) (map[string][]byte, string, error) {
	clean := map[string][]byte{}
	for p, b := range files {
		c, err := CleanPath(p)
		if err != nil {
			return nil, "", err
		}
		if skipFile(c) {
			continue
		}
		clean[c] = b
	}
	if len(clean) == 0 {
		return nil, "", core.BadRequest("no files to deploy")
	}
	// Strip one wrapping directory ("my-site/index.html" -> "index.html").
	for {
		prefix := ""
		same := true
		for p := range clean {
			i := strings.Index(p, "/")
			if i < 0 {
				same = false
				break
			}
			if prefix == "" {
				prefix = p[:i+1]
			} else if !strings.HasPrefix(p, prefix) {
				same = false
				break
			}
		}
		if !same || prefix == "" {
			break
		}
		next := map[string][]byte{}
		for p, b := range clean {
			next[strings.TrimPrefix(p, prefix)] = b
		}
		clean = next
	}
	// A project folder with build output: deploy the output.
	if _, hasIndex := clean["index.html"]; !hasIndex || clean["package.json"] != nil {
		for _, d := range []string{"dist/", "build/", "out/", "public/", "_site/", "site/", "docs/"} {
			if _, ok := clean[d+"index.html"]; ok {
				next := map[string][]byte{}
				for p, b := range clean {
					if strings.HasPrefix(p, d) {
						next[strings.TrimPrefix(p, d)] = b
					}
				}
				clean = next
				break
			}
		}
	}
	kind := ""
	trMu.Lock()
	trs := append([]Transformer(nil), transformers...)
	trMu.Unlock()
	for _, t := range trs {
		k, err := t.Apply(ctx, clean)
		if err != nil {
			return nil, "", err
		}
		if k != "" && kind == "" {
			kind = k
		}
	}
	if _, ok := clean["index.html"]; !ok {
		// A single HTML file with another name becomes the index.
		var htmls []string
		for p := range clean {
			if strings.HasSuffix(strings.ToLower(p), ".html") || strings.HasSuffix(strings.ToLower(p), ".htm") {
				htmls = append(htmls, p)
			}
		}
		sort.Strings(htmls)
		if len(htmls) == 1 && !strings.Contains(htmls[0], "/") {
			clean["index.html"] = clean[htmls[0]]
			if htmls[0] != "index.html" {
				delete(clean, htmls[0])
			}
		} else {
			clean["index.html"] = autoIndex(clean)
			if kind == "" {
				kind = "files"
			}
		}
	}
	if kind == "" {
		kind = "html"
	}
	return clean, kind, nil
}

func autoIndex(files map[string][]byte) []byte {
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Files</title><link rel="stylesheet" href="/_/kit.css"><main class="bk-page"><h1>Files</h1><ul class="bk-files">`)
	for _, p := range sortedKeys(files) {
		e := htmlEscape(p)
		b.WriteString(`<li><a href="` + e + `">` + e + `</a> <small>` + humanSize(int64(len(files[p]))) + `</small></li>`)
	}
	b.WriteString(`</ul></main>`)
	return []byte(b.String())
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ContentType guesses a file's MIME type.
func ContentType(p string, data []byte) string {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".txt", ".csv":
		return "text/plain; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json"
	case ".woff2":
		return "font/woff2"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

// Secret scanning blocks obvious credentials from being published.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{32,}\b`)},
	{"OpenAI API key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"Stripe secret key", regexp.MustCompile(`\bsk_live_[A-Za-z0-9]{20,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"private key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)},
	{"Azure storage key", regexp.MustCompile(`AccountKey=[A-Za-z0-9+/=]{60,}`)},
	{"Bunker token", regexp.MustCompile(`\bbk_[A-Za-z0-9_-]{40}\b`)},
}

// ScanSecrets returns a human message for the first secret found.
func ScanSecrets(files map[string][]byte) error {
	if core.EnvBool("ALLOW_SECRETS", false) {
		return nil
	}
	for _, p := range sortedKeys(files) {
		b := files[p]
		if len(b) > 5<<20 || !isTextPath(p) {
			continue
		}
		for _, sp := range secretPatterns {
			if loc := sp.re.FindIndex(b); loc != nil {
				line := bytes.Count(b[:loc[0]], []byte("\n")) + 1
				return core.NewErr(422, "secret_detected", fmt.Sprintf("%s (line %d) looks like %s %s. Remove it and try again: pages never need keys, Bunker calls services for you.", p, line, article(sp.name), sp.name))
			}
		}
	}
	return nil
}

func isTextPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm", ".js", ".mjs", ".css", ".json", ".txt", ".md", ".ts", ".tsx", ".jsx", ".env", ".yml", ".yaml", ".xml", ".svg", ".csv", ".ini", ".toml", ".vue", ".svelte":
		return true
	}
	return path.Ext(p) == ""
}

// ForbiddenUploads rejects file types that should never be hosted.
func checkTypes(files map[string][]byte) error {
	for p := range files {
		switch strings.ToLower(path.Ext(p)) {
		case ".exe", ".dll", ".msi", ".bat", ".cmd", ".ps1", ".scr", ".com", ".vbs", ".jar", ".app", ".dmg", ".pkg", ".deb", ".rpm", ".sh":
			return core.NewErr(415, "blocked_type", p+" is a program, and programs can't be uploaded to Bunker. Leave it out and try again")
		}
	}
	return nil
}

// StoreFiles writes files to the blob store and returns a manifest.
func StoreFiles(files map[string][]byte) (Manifest, error) {
	m := Manifest{}
	for p, b := range files {
		h, n, err := app.Blobs.PutBytes(b)
		if err != nil {
			return nil, err
		}
		m[p] = FileRef{Hash: h, Size: n, Type: ContentType(p, b)}
	}
	return m, nil
}

func siteMaxBytes() int64 { return int64(SettingInt("site_max_mb", 100)) << 20 }

// Commit creates a new version from a manifest. If publish is true it
// becomes the live version. The draft is cleared on publish.
func Commit(ctx context.Context, u *core.User, s *SiteRecord, m Manifest, message, source string, publish bool) (int64, error) {
	if len(m) == 0 {
		return 0, core.BadRequest("nothing to publish")
	}
	if m.Size() > siteMaxBytes() {
		return 0, core.TooLarge(fmt.Sprintf("this site would be %s, and the limit is %s. Leave out big files you don't need (like videos or backups) and try again", humanSize(m.Size()), humanSize(siteMaxBytes())))
	}
	for p, f := range m {
		if !app.Blobs.Exists(f.Hash) {
			return 0, core.BadRequest("missing content for " + p)
		}
	}
	raw, _ := json.Marshal(m)
	tx, err := app.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int64
	tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(n),0)+1 FROM versions WHERE site_id=?`, s.ID).Scan(&n)
	if message == "" {
		message = fmt.Sprintf("Version %d", n)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO versions(site_id,n,created_at,author_id,message,source,manifest,total_size,file_count) VALUES(?,?,?,?,?,?,?,?,?)`,
		s.ID, n, core.Now(), u.ID, trunc(message, 500), source, string(raw), m.Size(), len(m)); err != nil {
		return 0, err
	}
	if publish {
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET live_version=?, updated_at=? WHERE id=?`, n, core.Now(), s.ID); err != nil {
			return 0, err
		}
		tx.ExecContext(ctx, `DELETE FROM drafts WHERE site_id=?`, s.ID)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	Invalidate(s.Slug)
	ev := "site.publish"
	if !publish {
		ev = "site.version"
	}
	app.Audit(ctx, ev, s.ID, s.Slug, map[string]any{"version": n, "files": len(m), "bytes": m.Size(), "source": source})
	app.Emit(ctx, "site.published", map[string]any{"siteId": s.ID, "slug": s.Slug, "version": n})
	app.Bus.Publish("site:"+itoa64(s.ID)+":deploy", []byte(fmt.Sprintf(`{"type":"deploy","version":%d}`, n)))
	return n, nil
}

// SetKind records the detected site kind.
func SetKind(ctx context.Context, siteID int64, kind string) {
	if kind != "" {
		app.DB.ExecContext(ctx, `UPDATE sites SET kind=? WHERE id=?`, kind, siteID)
	}
}

// readUpload parses a deploy request body into files: multipart (files
// with relative paths in filename or a "path" field), a zip, a single
// file, or JSON {files:{path:{content|base64}}}.
// uploadSem bounds memory: uploads are parsed in memory, so only a few
// run at once (others wait their turn).
var uploadSem = make(chan struct{}, core.EnvInt("UPLOAD_CONCURRENCY", 4))

func readUpload(r *http.Request) (map[string][]byte, map[string]string, error) {
	select {
	case uploadSem <- struct{}{}:
		defer func() { <-uploadSem }()
	case <-r.Context().Done():
		return nil, nil, core.Unavailable("upload cancelled")
	}
	max := int64(SettingInt("upload_max_mb", 100)) << 20
	r.Body = http.MaxBytesReader(nil, r.Body, max)
	meta := map[string]string{}
	files := map[string][]byte{}
	ct := r.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	switch {
	case mt == "multipart/form-data":
		mr, err := r.MultipartReader()
		if err != nil {
			return nil, nil, core.BadRequest("bad multipart body")
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, nil, tooLargeOr(err, "upload failed")
			}
			var hint int64
			if strings.HasSuffix(strings.ToLower(part.FileName()), ".zip") {
				hint = min(r.ContentLength, max, 32<<20) // a zip is usually the whole body
			}
			data, err := readAllSized(part, hint)
			if err != nil {
				return nil, nil, tooLargeOr(err, "upload failed")
			}
			if part.FileName() == "" {
				meta[part.FormName()] = string(data)
				continue
			}
			name := part.FileName()
			// Browsers strip directories from filename; the UI sends the
			// relative path as the form name "file:<path>".
			if strings.HasPrefix(part.FormName(), "file:") {
				name = strings.TrimPrefix(part.FormName(), "file:")
			}
			if err := addUploaded(files, name, data); err != nil {
				return nil, nil, err
			}
		}
	case mt == "application/json":
		var in struct {
			Files   map[string]json.RawMessage `json:"files"`
			Message string                     `json:"message"`
			Publish *bool                      `json:"publish"`
			Title   string                     `json:"title"`
			Slug    string                     `json:"slug"`
			Source  string                     `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, nil, core.BadRequest("invalid JSON body")
		}
		for p, raw := range in.Files {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				files[p] = []byte(s)
				continue
			}
			var obj struct {
				Content *string `json:"content"`
				Base64  *string `json:"base64"`
			}
			if err := json.Unmarshal(raw, &obj); err != nil {
				return nil, nil, core.BadRequest("files values must be strings or {content}/{base64}")
			}
			if obj.Base64 != nil {
				b, err := base64.StdEncoding.DecodeString(*obj.Base64)
				if err != nil {
					return nil, nil, core.BadRequest("bad base64 for " + p)
				}
				files[p] = b
			} else if obj.Content != nil {
				files[p] = []byte(*obj.Content)
			}
		}
		meta["message"] = in.Message
		meta["title"], meta["slug"], meta["source"] = in.Title, in.Slug, in.Source
		if in.Publish != nil && !*in.Publish {
			meta["publish"] = "false"
		}
	default:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, nil, tooLargeOr(err, "upload failed")
		}
		name := r.URL.Query().Get("filename")
		if name == "" {
			if mt == "application/zip" || mt == "application/x-zip-compressed" {
				name = "upload.zip"
			} else if mt == "text/html" {
				name = "index.html"
			} else {
				name = "upload" + extFor(mt)
			}
		}
		if err := addUploaded(files, name, data); err != nil {
			return nil, nil, err
		}
	}
	for k, v := range r.URL.Query() {
		if meta[k] == "" && len(v) > 0 {
			meta[k] = v[0]
		}
	}
	return files, meta, nil
}

func extFor(mt string) string {
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func tooLargeOr(err error, msg string) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return core.TooLarge(fmt.Sprintf("that's more than this Bunker accepts in one upload (%s). Try fewer or smaller files, or ask your admin to raise the limit", humanSize(mbe.Limit)))
	}
	return core.BadRequest(msg)
}

func addUploaded(files map[string][]byte, name string, data []byte) error {
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return core.BadRequest(name + " isn't a valid zip file")
		}
		var total, read int64
		// One budget for the whole upload: everything already unpacked
		// counts, so many small zips can't each expand to the limit.
		for _, b := range files {
			read += int64(len(b))
		}
		total = read
		max := siteMaxBytes()
		dir := path.Dir(strings.ReplaceAll(name, "\\", "/"))
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			total += int64(f.UncompressedSize64)
			if total > max || len(zr.File) > 20000 {
				return core.TooLarge("this zip expands beyond the site size limit (" + humanSize(max) + "). Leave out big files you don't need and try again")
			}
			rc, err := f.Open()
			if err != nil {
				return core.BadRequest("can't read " + f.Name)
			}
			// Count real bytes: declared sizes in a zip can lie (zip bombs).
			b, err := readAllSized(io.LimitReader(rc, max-read+1), min(int64(f.UncompressedSize64), max-read, 16<<20))
			rc.Close()
			if err != nil {
				return core.BadRequest("can't read " + f.Name)
			}
			read += int64(len(b))
			if read > max {
				return core.TooLarge("this zip expands beyond the site size limit (" + humanSize(max) + "). Leave out big files you don't need and try again")
			}
			p := f.Name
			if dir != "." && dir != "" {
				p = dir + "/" + p
			}
			files[p] = b
		}
		return nil
	}
	files[name] = data
	return nil
}

// readAllSized is io.ReadAll starting from a buffer of hint bytes (when
// 0 < hint <= 256 MB), so a large upload isn't copied through a series
// of growing buffers. hint is only a capacity (callers cap it, so a lying
// size costs little): the data read decides.
func readAllSized(r io.Reader, hint int64) ([]byte, error) {
	if hint <= 0 || hint > 256<<20 {
		return io.ReadAll(r)
	}
	b := make([]byte, 0, hint+1) // +1: reaching EOF needs no growth
	for {
		n, err := r.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err == io.EOF {
			return b, nil
		}
		if err != nil {
			return b, err
		}
		if len(b) == cap(b) {
			b = append(b, 0)[:len(b)]
		}
	}
}

// deployFiles runs the full pipeline for uploaded files.
func deployFiles(ctx context.Context, u *core.User, s *SiteRecord, files map[string][]byte, message, source string, publish bool) (map[string]any, error) {
	ctx, warns := WithWarnings(ctx)
	res, err := deployFiles2(ctx, u, s, files, message, source, publish)
	if res != nil {
		if w := warns(); len(w) > 0 {
			res["warnings"] = w
		}
	}
	return res, err
}

func deployFiles2(ctx context.Context, u *core.User, s *SiteRecord, files map[string][]byte, message, source string, publish bool) (map[string]any, error) {
	files, kind, err := Normalize(ctx, files)
	if err != nil {
		return nil, err
	}
	if err := checkTypes(files); err != nil {
		return nil, err
	}
	if err := ScanSecrets(files); err != nil {
		app.Audit(ctx, "upload.blocked", s.ID, s.Slug, map[string]any{"reason": err.Error()})
		return nil, err
	}
	m, err := StoreFiles(files)
	if err != nil {
		return nil, err
	}
	if !publish {
		if err := SaveDraft(ctx, u, s, m); err != nil {
			return nil, err
		}
		SetKind(ctx, s.ID, kind)
		return map[string]any{"draft": true, "files": len(m), "kind": kind, "previewUrl": app.Cfg.URL + "/s/" + s.Slug + "/?draft=1"}, nil
	}
	n, err := Commit(ctx, u, s, m, message, source, true)
	if err != nil {
		return nil, err
	}
	SetKind(ctx, s.ID, kind)
	return map[string]any{"version": n, "files": len(m), "bytes": m.Size(), "kind": kind, "url": app.Cfg.URL + "/s/" + s.Slug + "/"}, nil
}

// SaveDraft stores the working copy used by the AI builder and editors.
func SaveDraft(ctx context.Context, u *core.User, s *SiteRecord, m Manifest) error {
	if m.Size() > siteMaxBytes() {
		return core.TooLarge("the draft is larger than the site size limit")
	}
	raw, _ := json.Marshal(m)
	_, err := app.DB.ExecContext(ctx, `INSERT INTO drafts(site_id,manifest,updated_at,updated_by) VALUES(?,?,?,?) ON CONFLICT(site_id) DO UPDATE SET manifest=excluded.manifest, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
		s.ID, string(raw), core.Now(), u.ID)
	if err == nil {
		app.Bus.Publish("site:"+itoa64(s.ID)+":draft", []byte(`{"type":"draft"}`))
	}
	return err
}

// WorkingManifest returns the draft if present, otherwise the live version (or empty).
func WorkingManifest(ctx context.Context, s *SiteRecord) (Manifest, bool, error) {
	m, err := LoadManifest(ctx, s.ID, 0)
	if err == nil {
		return m, true, nil
	}
	if s.Version > 0 {
		m, err := LoadManifest(ctx, s.ID, s.Version)
		if err != nil {
			return nil, false, err
		}
		cp := Manifest{}
		for k, v := range m {
			cp[k] = v
		}
		return cp, false, nil
	}
	return Manifest{}, false, nil
}

// GCBlobs removes blobs no manifest references (run after purges).
func GCBlobs(ctx context.Context) {
	used := map[string]bool{}
	for _, q := range []string{`SELECT manifest FROM versions`, `SELECT manifest FROM drafts`} {
		rows, err := app.DB.QueryContext(ctx, q)
		if err != nil {
			return
		}
		for rows.Next() {
			var raw string
			rows.Scan(&raw)
			var m Manifest
			json.Unmarshal([]byte(raw), &m)
			for _, f := range m {
				used[f.Hash] = true
			}
		}
		rows.Close()
	}
	app.Emit(ctx, "blobs.gc", used) // other modules add their references
	_ = used
}

func registerDeploy(a *core.App) {
	a.Handle(core.Op{Name: "sites.deploy", Method: "POST", Path: "/api/v1/sites/{site}/deploy", Summary: "Upload files (zip, folder, single file or JSON) and publish them", Body: "multipart",
		Params: []core.Param{siteParam,
			{Name: "files", In: "body", Type: "file", Required: true, Desc: "Files, a .zip, or JSON {files:{path:content}}"},
			{Name: "message", In: "body", Desc: "What changed"},
			{Name: "publish", In: "body", Type: "boolean", Desc: "Publish now (default true); false saves a draft"},
		},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			files, meta, err := readUpload(r)
			if err != nil {
				return nil, err
			}
			src := firstNonEmpty(meta["source"], "upload")
			if u.ViaToken && src == "upload" {
				src = "api"
			}
			return deployFiles(r.Context(), u, s, files, meta["message"], src, meta["publish"] != "false")
		}})

	// Quick start: create + deploy in one call (used by drag-and-drop on
	// the home page, the CLI's first deploy and agents).
	a.Handle(core.Op{Name: "sites.drop", Method: "POST", Path: "/api/v1/drop", Summary: "Create a new site from dropped files in one step", Body: "multipart",
		Params: []core.Param{
			{Name: "files", In: "body", Type: "file", Required: true},
			{Name: "title", In: "body", Desc: "Name (guessed from the files if empty)"},
			{Name: "slug", In: "body"},
		},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			files, meta, err := readUpload(r)
			if err != nil {
				return nil, err
			}
			title := meta["title"]
			if title == "" {
				title = guessTitle(files)
			}
			s, err := CreateSite(r.Context(), u, title, meta["slug"], "", "")
			if err != nil {
				return nil, err
			}
			res, err := deployFiles(r.Context(), u, s, files, "First version", firstNonEmpty(meta["source"], "drop"), true)
			if err != nil {
				// Don't leave an empty site behind.
				a.DB.ExecContext(r.Context(), `DELETE FROM sites WHERE id=?`, s.ID)
				return nil, err
			}
			res["slug"] = s.Slug
			res["title"] = s.Title
			return core.Created{V: res}, nil
		}})

	// Content-addressed incremental deploys for the CLI: send the manifest,
	// upload only missing blobs, then commit.
	a.Handle(core.Op{Name: "blobs.missing", Method: "POST", Path: "/api/v1/blobs/missing", Summary: "Which of these content hashes does the server need?", Hidden: true,
		Fn: func(r *http.Request) (any, error) {
			var in struct{ Hashes []string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			out := []string{}
			for _, h := range in.Hashes {
				if !app.Blobs.Exists(h) {
					out = append(out, h)
				}
			}
			return map[string]any{"missing": out}, nil
		}})
	a.Handle(core.Op{Name: "blobs.put", Method: "PUT", Path: "/api/v1/blobs/{hash}", Summary: "Upload one content blob", Hidden: true, Body: "raw",
		Fn: func(r *http.Request) (any, error) {
			want := r.PathValue("hash")
			h, n, err := app.Blobs.Put(r.Body, siteMaxBytes())
			if err != nil {
				return nil, err
			}
			if h != want {
				return nil, core.BadRequest("content hash mismatch")
			}
			return map[string]any{"hash": h, "size": n}, nil
		}})
	a.Handle(core.Op{Name: "sites.commit", Method: "POST", Path: "/api/v1/sites/{site}/commit", Summary: "Publish a manifest of already-uploaded blobs", Hidden: true,
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct {
				Files   map[string]string `json:"files"` // path -> hash
				Message string
				Publish *bool
				Source  string
			}
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			m := Manifest{}
			raw := map[string][]byte{}
			for p, h := range in.Files {
				c, err := CleanPath(p)
				if err != nil {
					return nil, err
				}
				if skipFile(c) {
					continue
				}
				b, err := app.Blobs.Read(h)
				if err != nil {
					return nil, core.BadRequest("missing content for " + p + "; upload it first")
				}
				raw[c] = b
				m[c] = FileRef{Hash: h, Size: int64(len(b)), Type: ContentType(c, b)}
			}
			if err := checkTypes(raw); err != nil {
				return nil, err
			}
			if err := ScanSecrets(raw); err != nil {
				return nil, err
			}
			if _, ok := m["index.html"]; !ok {
				return nil, core.BadRequest("index.html is required")
			}
			src := firstNonEmpty(in.Source, "cli")
			if in.Publish != nil && !*in.Publish {
				return map[string]any{"draft": true}, SaveDraft(r.Context(), u, s, m)
			}
			n, err := Commit(r.Context(), u, s, m, in.Message, src, true)
			if err != nil {
				return nil, err
			}
			return map[string]any{"version": n, "url": app.Cfg.URL + "/s/" + s.Slug + "/"}, nil
		}})

	// ---- versions ----
	a.Handle(core.Op{Name: "versions.list", Method: "GET", Path: "/api/v1/sites/{site}/versions", Summary: "List a site's versions", Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			rows, err := a.DB.QueryContext(r.Context(), `SELECT v.n,v.created_at,v.message,v.source,v.total_size,v.file_count,COALESCE(u.name,''),COALESCE(u.email,'') FROM versions v LEFT JOIN users u ON u.id=v.author_id WHERE v.site_id=? ORDER BY v.n DESC LIMIT 500`, s.ID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			type ver struct {
				N         int64  `json:"n"`
				CreatedAt int64  `json:"createdAt"`
				Message   string `json:"message"`
				Source    string `json:"source"`
				Size      int64  `json:"size"`
				Files     int    `json:"files"`
				Author    string `json:"author"`
				Email     string `json:"authorEmail"`
				Live      bool   `json:"live"`
			}
			out := []ver{}
			for rows.Next() {
				var v ver
				rows.Scan(&v.N, &v.CreatedAt, &v.Message, &v.Source, &v.Size, &v.Files, &v.Author, &v.Email)
				v.Live = v.N == s.Version
				out = append(out, v)
			}
			return map[string]any{"live": s.Version, "versions": out}, nil
		}})
	a.Handle(core.Op{Name: "versions.restore", Method: "POST", Path: "/api/v1/sites/{site}/versions/{n}/restore", Summary: "Make an old version live again (creates a new version)",
		Params: []core.Param{siteParam, {Name: "n", In: "path", Type: "integer", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			n, _ := strconv.ParseInt(r.PathValue("n"), 10, 64)
			m, err := LoadManifest(r.Context(), s.ID, n)
			if err != nil || n == 0 {
				return nil, core.NotFound("version not found")
			}
			nv, err := Commit(r.Context(), u, s, m, fmt.Sprintf("Restored version %d", n), "restore", true)
			if err != nil {
				return nil, err
			}
			return map[string]any{"version": nv}, nil
		}})
	a.Handle(core.Op{Name: "versions.diff", Method: "GET", Path: "/api/v1/sites/{site}/versions/{n}/diff", Summary: "Which files changed in a version compared to another",
		Params: []core.Param{siteParam, {Name: "n", In: "path", Type: "integer", Required: true}, {Name: "against", In: "query", Type: "integer", Desc: "Compare with this version (default: previous; 0 = draft)"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			n, _ := strconv.ParseInt(r.PathValue("n"), 10, 64)
			ag := n - 1
			if v := r.URL.Query().Get("against"); v != "" {
				ag, _ = strconv.ParseInt(v, 10, 64)
			}
			a1, err := LoadManifest(r.Context(), s.ID, n)
			if err != nil {
				return nil, err
			}
			a0 := Manifest{}
			if ag > 0 || r.URL.Query().Get("against") == "0" {
				if m, err := LoadManifest(r.Context(), s.ID, ag); err == nil {
					a0 = m
				}
			}
			added, removed, changed := []string{}, []string{}, []string{}
			for p, f := range a1 {
				if o, ok := a0[p]; !ok {
					added = append(added, p)
				} else if o.Hash != f.Hash {
					changed = append(changed, p)
				}
			}
			for p := range a0 {
				if _, ok := a1[p]; !ok {
					removed = append(removed, p)
				}
			}
			sort.Strings(added)
			sort.Strings(removed)
			sort.Strings(changed)
			return map[string]any{"added": added, "removed": removed, "changed": changed}, nil
		}})

	// ---- files of a version or the draft (for editors, the AI builder and agents) ----
	a.Handle(core.Op{Name: "files.list", Method: "GET", Path: "/api/v1/sites/{site}/files", Summary: "List the site's files (draft if one exists, else live)",
		Params: []core.Param{siteParam, {Name: "version", In: "query", Type: "integer", Desc: "A specific version"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var m Manifest
			draft := false
			if v := r.URL.Query().Get("version"); v != "" {
				n, _ := strconv.ParseInt(v, 10, 64)
				m, err = LoadManifest(r.Context(), s.ID, n)
			} else {
				m, draft, err = WorkingManifest(r.Context(), s)
			}
			if err != nil {
				return nil, err
			}
			type fi struct {
				Path string `json:"path"`
				Size int64  `json:"size"`
				Type string `json:"type"`
				Hash string `json:"hash"`
			}
			out := []fi{}
			for _, p := range m.Paths() {
				f := m[p]
				out = append(out, fi{p, f.Size, f.Type, f.Hash})
			}
			return map[string]any{"draft": draft, "files": out}, nil
		}})
	a.Handle(core.Op{Name: "files.read", Method: "GET", Path: "/api/v1/sites/{site}/files/{path...}", Summary: "Read one file's content",
		Params: []core.Param{siteParam, {Name: "path", In: "path", Required: true}, {Name: "version", In: "query", Type: "integer"}},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				core.Err(w, err)
				return
			}
			var m Manifest
			if v := r.URL.Query().Get("version"); v != "" {
				n, _ := strconv.ParseInt(v, 10, 64)
				m, err = LoadManifest(r.Context(), s.ID, n)
			} else {
				m, _, err = WorkingManifest(r.Context(), s)
			}
			if err != nil {
				core.Err(w, err)
				return
			}
			p, err := CleanPath(r.PathValue("path"))
			if err != nil {
				core.Err(w, err)
				return
			}
			f, ok := m[p]
			if !ok {
				core.Err(w, core.NotFound("no file "+p))
				return
			}
			b, err := app.Blobs.Read(f.Hash)
			if err != nil {
				core.Err(w, core.NotFound("content missing"))
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			if isTextPath(p) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			}
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(b)
		}})
	a.Handle(core.Op{Name: "files.write", Method: "PUT", Path: "/api/v1/sites/{site}/files/{path...}", Summary: "Create or replace a file in the draft", Body: "raw",
		Params: []core.Param{siteParam, {Name: "path", In: "path", Required: true}, {Name: "content", In: "body", Type: "file", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			p, err := CleanPath(r.PathValue("path"))
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, siteMaxBytes()))
			if err != nil {
				return nil, core.TooLarge("file too large")
			}
			if err := checkTypes(map[string][]byte{p: b}); err != nil {
				return nil, err
			}
			if err := ScanSecrets(map[string][]byte{p: b}); err != nil {
				return nil, err
			}
			m, _, err := WorkingManifest(r.Context(), s)
			if err != nil {
				return nil, err
			}
			h, n, err := app.Blobs.PutBytes(b)
			if err != nil {
				return nil, err
			}
			m[p] = FileRef{Hash: h, Size: n, Type: ContentType(p, b)}
			if err := SaveDraft(r.Context(), u, s, m); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true, "path": p, "size": n, "draft": true}, nil
		}})
	a.Handle(core.Op{Name: "files.delete", Method: "DELETE", Path: "/api/v1/sites/{site}/files/{path...}", Summary: "Delete a file from the draft", Destructive: true,
		Params: []core.Param{siteParam, {Name: "path", In: "path", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			p, err := CleanPath(r.PathValue("path"))
			if err != nil {
				return nil, err
			}
			m, _, err := WorkingManifest(r.Context(), s)
			if err != nil {
				return nil, err
			}
			if _, ok := m[p]; !ok {
				return nil, core.NotFound("no file " + p)
			}
			delete(m, p)
			return map[string]any{"ok": true}, SaveDraft(r.Context(), u, s, m)
		}})
	a.Handle(core.Op{Name: "drafts.publish", Method: "POST", Path: "/api/v1/sites/{site}/draft/publish", Summary: "Publish the draft as a new live version",
		Params: []core.Param{siteParam, {Name: "message", In: "body", Desc: "What changed"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct{ Message string }
			_ = core.Decode(r, &in)
			m, err := LoadManifest(r.Context(), s.ID, 0)
			if err != nil {
				return nil, core.BadRequest("there's no draft to publish")
			}
			if _, ok := m["index.html"]; !ok {
				return nil, core.BadRequest("add an index.html before publishing")
			}
			n, err := Commit(r.Context(), u, s, m, in.Message, "draft", true)
			if err != nil {
				return nil, err
			}
			return map[string]any{"version": n, "url": app.Cfg.URL + "/s/" + s.Slug + "/"}, nil
		}})
	a.Handle(core.Op{Name: "drafts.discard", Method: "DELETE", Path: "/api/v1/sites/{site}/draft", Summary: "Throw away unpublished changes", Destructive: true,
		Params: []core.Param{siteParam},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := Full(r.Context(), core.MustUser(r), r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			a.DB.ExecContext(r.Context(), `DELETE FROM drafts WHERE site_id=?`, s.ID)
			app.Bus.Publish("site:"+itoa64(s.ID)+":draft", []byte(`{"type":"draft"}`))
			return map[string]any{"ok": true}, nil
		}})
}

func guessTitle(files map[string][]byte) string {
	keys := sortedKeys(files)
	htmlTitle := func(p string) string {
		if m := titleRe.FindSubmatch(files[p]); m != nil {
			t := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
			if t != "" && len(t) < 100 {
				return t
			}
		}
		return ""
	}
	// The start page's <title> wins, then any page's.
	for _, p := range keys {
		if b := strings.ToLower(path.Base(p)); (b == "index.html" || b == "index.htm") && strings.Count(p, "/") <= 1 {
			if t := htmlTitle(p); t != "" {
				return t
			}
		}
	}
	for _, p := range keys {
		if l := strings.ToLower(p); strings.HasSuffix(l, ".html") || strings.HasSuffix(l, ".htm") {
			if t := htmlTitle(p); t != "" {
				return t
			}
		}
	}
	// Markdown: the main document's first "# Heading".
	mds := 0
	for _, p := range keys {
		if l := strings.ToLower(p); strings.HasSuffix(l, ".md") || strings.HasSuffix(l, ".markdown") {
			mds++
		}
	}
	for _, p := range keys {
		b := strings.ToLower(path.Base(p))
		if (mds == 1 && (strings.HasSuffix(b, ".md") || strings.HasSuffix(b, ".markdown"))) || ((b == "readme.md" || b == "index.md") && strings.Count(p, "/") <= 1) {
			if m := fmTitleRe.FindSubmatch(files[p]); m != nil {
				if t := strings.TrimSpace(string(m[1])); t != "" && len(t) < 100 {
					return t
				}
			}
			if m := mdTitleRe.FindSubmatch(files[p]); m != nil {
				if t := strings.TrimSpace(string(m[1])); t != "" && len(t) < 100 {
					return t
				}
			}
		}
	}
	if len(keys) == 0 {
		return "Untitled site"
	}
	// A dropped folder: use its name.
	if top, _, ok := strings.Cut(keys[0], "/"); ok {
		same := true
		for _, p := range keys {
			if !strings.HasPrefix(p, top+"/") {
				same = false
				break
			}
		}
		if same && top != "" && top != "dist" && top != "build" && top != "public" && top != "out" {
			return niceTitle(top)
		}
	}
	images := 0
	for _, p := range keys {
		switch strings.ToLower(path.Ext(p)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".avif":
			images++
		}
	}
	if images > 1 && images == len(keys) {
		return "Gallery"
	}
	for _, p := range keys {
		base := path.Base(p)
		if i := strings.LastIndex(base, "."); i > 0 {
			base = base[:i]
		}
		if base != "index" && base != "" && base != "README" && base != "readme" {
			return niceTitle(base)
		}
	}
	return "Untitled site"
}

// article picks "a" or "an" for a word.
func article(w string) string {
	if w != "" && strings.ContainsRune("AEIOUaeiou", rune(w[0])) {
		return "an"
	}
	return "a"
}

// niceTitle turns a file or folder name like "q3-sales_report" into "Q3 sales report".
func niceTitle(s string) string {
	s = strings.Join(strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(s)), " ")
	if s == "" {
		return "Untitled site"
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var fmTitleRe = regexp.MustCompile(`(?s)\A---\r?\n(?:[^\n]*\n)*?(?:site|title):[ \t]*["']?([^"'\r\n]+?)["']?[ \t]*\r?\n`)
var mdTitleRe = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*\r?$`)

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

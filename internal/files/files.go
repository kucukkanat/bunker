// Package files stores uploads made by site pages (bunker.files) in the
// content-addressed blob store and serves them through ticketed URLs
// under /s/<slug>/~f/<ticket>/<id>/<name>.
package files

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/data"
	"github.com/kucukkanat/bunker/internal/sites"
)

func init() {
	core.Register(core.Module{
		Name:  "files",
		Order: 45,
		Migrations: []string{`
CREATE TABLE uploads (
  site_id INTEGER NOT NULL,
  id TEXT NOT NULL,
  name TEXT NOT NULL,
  folder TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL,
  type TEXT NOT NULL,
  hash TEXT NOT NULL,
  author_id INTEGER NOT NULL,
  author_name TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY(site_id, id)
);
CREATE INDEX uploads_folder ON uploads(site_id, folder, created_at);
CREATE INDEX uploads_author ON uploads(site_id, author_id, created_at);
CREATE INDEX uploads_hash ON uploads(hash);
CREATE TABLE upload_rules (
  site_id INTEGER PRIMARY KEY,
  rule TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
`},
		Init: initFiles,
	})
}

var app *core.App

func maxFileBytes() int64 { return int64(core.EnvInt("UPLOAD_MAX_MB", 100)) << 20 }
func siteMaxBytes() int64 { return int64(core.EnvInt("UPLOADS_SITE_MAX_MB", 2048)) << 20 }
func siteMaxFiles() int64 { return int64(core.EnvInt("UPLOADS_MAX_FILES", 20000)) }
func nowMS() int64        { return time.Now().UnixMilli() }
func i64(n int64) string  { return strconv.FormatInt(n, 10) }

var siteParam = core.Param{Name: "site", In: "path", Required: true, Desc: "Site address (slug)"}

func initFiles(a *core.App) error {
	app = a
	registerOps(a)
	sites.RegisterRawPrefix("~f/", serveFile)
	a.On("blobs.gc", func(ctx context.Context, p any) {
		used, ok := p.(map[string]bool)
		if !ok {
			return
		}
		rows, err := a.DB.QueryContext(context.WithoutCancel(ctx), `SELECT DISTINCT hash FROM uploads`)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var h string
			rows.Scan(&h)
			used[h] = true
		}
	})
	a.On("site.purge", func(ctx context.Context, p any) {
		if id, ok := p.(int64); ok {
			a.DB.ExecContext(context.WithoutCancel(ctx), `DELETE FROM uploads WHERE site_id=?`, id)
			a.DB.ExecContext(context.WithoutCancel(ctx), `DELETE FROM upload_rules WHERE site_id=?`, id)
		}
	})
	return nil
}

// ---- types ----

type kindInfo struct {
	ct     string
	inline bool
}

// allowed maps extensions to the served Content-Type. Anything else is refused.
var allowed = map[string]kindInfo{
	// images
	".png": {"image/png", true}, ".jpg": {"image/jpeg", true}, ".jpeg": {"image/jpeg", true}, ".gif": {"image/gif", true},
	".webp": {"image/webp", true}, ".avif": {"image/avif", true}, ".svg": {"image/svg+xml", true}, ".bmp": {"image/bmp", true},
	".ico": {"image/x-icon", true}, ".heic": {"image/heic", true}, ".heif": {"image/heif", true}, ".tif": {"image/tiff", true}, ".tiff": {"image/tiff", true},
	// documents
	".pdf": {"application/pdf", true},
	".doc": {"application/msword", false}, ".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", false},
	".xls": {"application/vnd.ms-excel", false}, ".xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", false},
	".ppt": {"application/vnd.ms-powerpoint", false}, ".pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", false},
	".odt": {"application/vnd.oasis.opendocument.text", false}, ".ods": {"application/vnd.oasis.opendocument.spreadsheet", false},
	".odp": {"application/vnd.oasis.opendocument.presentation", false}, ".rtf": {"application/rtf", false},
	// video / audio
	".mp4": {"video/mp4", true}, ".webm": {"video/webm", true}, ".mov": {"video/quicktime", true}, ".m4v": {"video/x-m4v", true}, ".ogv": {"video/ogg", true},
	".mp3": {"audio/mpeg", true}, ".wav": {"audio/wav", true}, ".ogg": {"audio/ogg", true}, ".oga": {"audio/ogg", true}, ".m4a": {"audio/mp4", true},
	".aac": {"audio/aac", true}, ".flac": {"audio/flac", true}, ".opus": {"audio/opus", true}, ".weba": {"audio/webm", true},
	// text and data
	".txt": {"text/plain; charset=utf-8", false}, ".csv": {"text/csv; charset=utf-8", false}, ".tsv": {"text/tab-separated-values; charset=utf-8", false},
	".md": {"text/markdown; charset=utf-8", false}, ".json": {"application/json", false}, ".xml": {"application/xml", false},
	".log": {"text/plain; charset=utf-8", false}, ".ics": {"text/calendar", false}, ".vcf": {"text/vcard", false},
	// archives
	".zip": {"application/zip", false},
}

// typeFor returns the served type for a file name, or an error for blocked types.
func typeFor(name string) (kindInfo, error) {
	ext := strings.ToLower(path.Ext(name))
	k, ok := allowed[ext]
	if !ok {
		if ext == "" {
			return k, core.NewErr(415, "blocked_type", "files need an extension like .pdf or .png so Bunker knows what they are")
		}
		return k, core.NewErr(415, "blocked_type", ext+" files can't be uploaded here. Allowed: images, PDF, video, audio, Office documents, text/CSV and zip.")
	}
	return k, nil
}

// looksExecutable sniffs common executable and script headers.
func looksExecutable(head []byte) bool {
	switch {
	case bytes.HasPrefix(head, []byte("MZ")), // Windows PE
		bytes.HasPrefix(head, []byte("\x7fELF")),
		bytes.HasPrefix(head, []byte("\xfe\xed\xfa")), bytes.HasPrefix(head, []byte("\xcf\xfa\xed\xfe")), bytes.HasPrefix(head, []byte("\xce\xfa\xed\xfe")),
		bytes.HasPrefix(head, []byte("\xca\xfe\xba\xbe")), // Mach-O fat / Java class
		bytes.HasPrefix(head, []byte("#!")):
		return true
	}
	return false
}

// cleanName keeps a safe display file name.
func cleanName(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = path.Base(n)
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '"' || r == '<' || r == '>' || r == '|' || r == '?' || r == '*' || r == ':' {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimSpace(strings.Trim(n, "."+" "))
	if !utf8.ValidString(n) {
		n = strings.ToValidUTF8(n, "_")
	}
	if len(n) > 200 {
		ext := path.Ext(n)
		if len(ext) > 16 {
			ext = ""
		}
		n = strings.ToValidUTF8(n[:200-len(ext)], "") + ext
	}
	if n == "" || n == "." {
		n = "file"
	}
	return n
}

func cleanFolder(f string) (string, error) {
	f = strings.Trim(strings.ReplaceAll(f, "\\", "/"), "/ ")
	if f == "" {
		return "", nil
	}
	c := path.Clean(f)
	if c == "." || strings.HasPrefix(c, "..") || len(c) > 200 || strings.ContainsAny(c, "\x00\"<>|?*:") {
		return "", core.BadRequest("folder names use letters, numbers and slashes, like receipts/2026")
	}
	return c, nil
}

// ---- records ----

// File is an upload as returned by the API.
type File struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Folder     string `json:"folder"`
	Size       int64  `json:"size"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Author     int64  `json:"_author"`
	AuthorName string `json:"_authorName"`
	Created    int64  `json:"-"`
	CreatedISO string `json:"_created"`
	hash       string
}

const cols = `id, name, folder, size, type, hash, author_id, author_name, created_at`

func scan(sc interface{ Scan(...any) error }) (*File, error) {
	f := &File{}
	err := sc.Scan(&f.ID, &f.Name, &f.Folder, &f.Size, &f.Type, &f.hash, &f.Author, &f.AuthorName, &f.Created)
	f.CreatedISO = data.ISOTime(f.Created)
	return f, err
}

// URL builds the ticketed content URL. The ticket's version field is not
// meaningful for files; the site's live version keeps it a valid ticket.
func fileURL(s *core.Site, uid int64, f *File) string {
	ver := s.Version
	if ver < 0 {
		ver = 0
	}
	return "/s/" + s.Slug + "/~f/" + sites.MakeTicket(s.ID, ver, uid) + "/" + f.ID + "/" + url.PathEscape(f.Name)
}

// Rule returns the uploads rule for a site.
func Rule(ctx context.Context, siteID int64) string {
	rule := data.RuleOwn
	app.DB.QueryRowContext(ctx, `SELECT rule FROM upload_rules WHERE site_id=?`, siteID).Scan(&rule)
	return rule
}

func getFile(ctx context.Context, siteID int64, id string) (*File, error) {
	f, err := scan(app.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM uploads WHERE site_id=? AND id=?`, siteID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.NotFound("that file doesn't exist (it may have been removed)")
	}
	return f, err
}

// ---- ops ----

func registerOps(a *core.App) {
	idParam := core.Param{Name: "id", In: "path", Required: true, Desc: "File id"}

	a.Handle(core.Op{Name: "uploads.list", Method: "GET", Path: "/api/v1/sites/{site}/uploads", Summary: "List files uploaded on a site", PageSafe: true,
		Params: []core.Param{siteParam,
			{Name: "folder", In: "query", Desc: "Only this folder"},
			{Name: "mine", In: "query", Type: "boolean", Desc: "Only files you uploaded"},
			{Name: "limit", In: "query", Type: "integer", Desc: "1–500 (default 200)"},
			{Name: "before", In: "query", Type: "integer", Desc: "Cursor: the previous page's next value"},
		},
		Fn: func(r *http.Request) (any, error) {
			s, role, err := a.SiteFor(r, core.RoleViewer)
			if err != nil {
				return nil, err
			}
			u := core.UserFrom(r.Context())
			rule := Rule(r.Context(), s.ID)
			acc := data.AccessFor(u, role, rule)
			qv := r.URL.Query()
			q := `SELECT ` + cols + ` FROM uploads WHERE site_id=?`
			args := []any{s.ID}
			if !acc.ReadAll || qv.Get("mine") == "1" || qv.Get("mine") == "true" {
				q += ` AND author_id=?`
				args = append(args, u.ID)
			}
			if f := qv.Get("folder"); f != "" {
				folder, err := cleanFolder(f)
				if err != nil {
					return nil, err
				}
				q += ` AND folder=?`
				args = append(args, folder)
			}
			if b := qv.Get("before"); b != "" {
				n, err := strconv.ParseInt(b, 10, 64)
				if err != nil {
					return nil, core.BadRequest("before must be a number")
				}
				q += ` AND created_at < ?`
				args = append(args, n)
			}
			limit := 200
			if l, err := strconv.Atoi(qv.Get("limit")); err == nil && l > 0 && l <= 500 {
				limit = l
			}
			q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
			args = append(args, limit+1)
			rows, err := a.DB.QueryContext(r.Context(), q, args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []*File{}
			var next *int64
			for rows.Next() {
				f, err := scan(rows)
				if err != nil {
					return nil, err
				}
				if len(out) == limit {
					c := out[len(out)-1].Created
					next = &c
					break
				}
				f.URL = fileURL(s, u.ID, f)
				out = append(out, f)
			}
			return map[string]any{"items": out, "next": next, "rule": rule}, nil
		}})

	a.Handle(core.Op{Name: "uploads.get", Method: "GET", Path: "/api/v1/sites/{site}/uploads/{id}", Summary: "Get one uploaded file's details", PageSafe: true,
		Params: []core.Param{siteParam, idParam},
		Fn: func(r *http.Request) (any, error) {
			s, f, _, err := readable(r)
			if err != nil {
				return nil, err
			}
			f.URL = fileURL(s, core.UserFrom(r.Context()).ID, f)
			return f, nil
		}})

	a.Handle(core.Op{Name: "uploads.content", Method: "GET", Path: "/api/v1/sites/{site}/uploads/{id}/content", Summary: "Download an uploaded file's content", PageSafe: true,
		Params: []core.Param{siteParam, idParam},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			_, f, _, err := readable(r)
			if err != nil {
				core.Err(w, err)
				return
			}
			fh, err := app.Blobs.Open(f.hash)
			if err != nil {
				core.Err(w, core.NotFound("this file's content is missing"))
				return
			}
			defer fh.Close()
			w.Header().Set("Content-Type", f.Type)
			w.Header().Set("Cache-Control", "private, no-cache")
			w.Header().Set("Content-Disposition", disposition(f, false))
			w.Header().Set("ETag", `"`+f.hash[:24]+`"`)
			http.ServeContent(w, r, "", time.Time{}, fh)
		}})

	a.Handle(core.Op{Name: "uploads.upload", Method: "POST", Path: "/api/v1/sites/{site}/uploads", Summary: "Upload one or more files", PageSafe: true, Body: "multipart",
		Params: []core.Param{siteParam,
			{Name: "file", In: "body", Type: "file", Required: true, Desc: "One or more files"},
			{Name: "folder", In: "body", Desc: "Folder to put them in (optional)"},
		},
		Fn: upload})

	a.Handle(core.Op{Name: "uploads.delete", Method: "DELETE", Path: "/api/v1/sites/{site}/uploads/{id}", Summary: "Remove an uploaded file", PageSafe: true, Destructive: true,
		Params: []core.Param{siteParam, idParam},
		Fn: func(r *http.Request) (any, error) {
			s, f, acc, err := readable(r)
			if err != nil {
				return nil, err
			}
			u := core.UserFrom(r.Context())
			if !acc.CanModify(u.ID, f.Author) {
				return nil, core.Forbidden("you can't remove this file. " + data.RuleHelp(Rule(r.Context(), s.ID)))
			}
			if _, err := a.DB.ExecContext(r.Context(), `DELETE FROM uploads WHERE site_id=? AND id=?`, s.ID, f.ID); err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "upload.delete", s.ID, f.Name, map[string]any{"id": f.ID, "size": f.Size})
			return nil, nil
		}})

	a.Handle(core.Op{Name: "uploads.rule", Method: "PUT", Path: "/api/v1/sites/{site}/uploads/_rule", Summary: "Set who can see and remove uploads (editors only)",
		Params: []core.Param{siteParam, {Name: "rule", In: "body", Required: true, Desc: "own | private | read | append | open | group:<name>"}},
		Fn: func(r *http.Request) (any, error) {
			s, _, err := a.SiteFor(r, core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct{ Rule string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			rule, err := data.ValidRule(in.Rule)
			if err != nil {
				return nil, err
			}
			was := Rule(r.Context(), s.ID)
			if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO upload_rules(site_id,rule,updated_at) VALUES(?,?,?) ON CONFLICT(site_id) DO UPDATE SET rule=excluded.rule, updated_at=excluded.updated_at`, s.ID, rule, nowMS()); err != nil {
				return nil, err
			}
			a.Audit(r.Context(), "upload.rule", s.ID, "", map[string]any{"rule": rule, "was": was})
			return map[string]any{"rule": rule}, nil
		}})
}

// readable loads {id} and checks the caller may see it.
func readable(r *http.Request) (*core.Site, *File, data.Access, error) {
	s, role, err := app.SiteFor(r, core.RoleViewer)
	if err != nil {
		return nil, nil, data.Access{}, err
	}
	u := core.UserFrom(r.Context())
	acc := data.AccessFor(u, role, Rule(r.Context(), s.ID))
	f, err := getFile(r.Context(), s.ID, r.PathValue("id"))
	if err != nil {
		return nil, nil, acc, err
	}
	if !acc.CanRead(u.ID, f.Author) {
		return nil, nil, acc, core.NotFound("that file doesn't exist (it may have been removed)")
	}
	return s, f, acc, nil
}

func upload(r *http.Request) (any, error) {
	s, role, err := app.SiteFor(r, core.RoleViewer)
	if err != nil {
		return nil, err
	}
	u := core.UserFrom(r.Context())
	ctx := r.Context()
	rule := Rule(ctx, s.ID)
	if !data.AccessFor(u, role, rule).Create {
		return nil, core.Forbidden("you can't upload files to this site. " + data.RuleHelp(rule))
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "multipart/form-data" {
		return nil, core.BadRequest("upload files as multipart/form-data")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxFileBytes()*4+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, core.BadRequest("bad upload")
	}
	// last: the newest upload's time. Each upload gets a distinct created_at
	// so the list cursor (created_at < before) never skips files stored in
	// the same millisecond (several files in one request).
	var used, count, last int64
	app.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0), COUNT(*), COALESCE(MAX(created_at),0) FROM uploads WHERE site_id=?`, s.ID).Scan(&used, &count, &last)
	folder := r.URL.Query().Get("folder")
	var out []*File
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, core.TooLarge("that upload is too large")
			}
			return nil, core.BadRequest("the upload was interrupted; try again")
		}
		if part.FileName() == "" {
			if part.FormName() == "folder" {
				b, _ := io.ReadAll(io.LimitReader(part, 512))
				folder = string(b)
			}
			part.Close()
			continue
		}
		if len(out) >= 20 {
			return nil, core.BadRequest("upload 20 files at a time at most")
		}
		fold, err := cleanFolder(folder)
		if err != nil {
			return nil, err
		}
		name := cleanName(part.FileName())
		k, err := typeFor(name)
		if err != nil {
			return nil, err
		}
		br := bufio.NewReaderSize(part, 4096)
		head, _ := br.Peek(512)
		if looksExecutable(head) {
			app.Audit(ctx, "upload.blocked", s.ID, name, map[string]any{"reason": "executable"})
			return nil, core.NewErr(415, "blocked_type", name+" looks like a program, and programs can't be uploaded")
		}
		if count+1 > siteMaxFiles() {
			return nil, core.Quota("this site has reached its file limit. Remove some files first.")
		}
		hash, size, err := app.Blobs.Put(br, maxFileBytes())
		if err != nil {
			var he *core.HTTPError
			if errors.As(err, &he) {
				return nil, core.TooLarge(name + " is larger than " + i64(maxFileBytes()>>20) + " MB")
			}
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, core.TooLarge("that upload is too large")
			}
			return nil, core.BadRequest("the upload was interrupted; try again")
		}
		if size == 0 {
			return nil, core.BadRequest(name + " is empty")
		}
		if used+size > siteMaxBytes() {
			return nil, core.Quota("this site's file storage is full (" + i64(siteMaxBytes()>>20) + " MB). Remove some files first.")
		}
		f := &File{ID: data.NewID(), Name: name, Folder: fold, Size: size, Type: k.ct, hash: hash, Author: u.ID, AuthorName: u.Name, Created: max(nowMS(), last+1)}
		last = f.Created
		f.CreatedISO = data.ISOTime(f.Created)
		if _, err := app.DB.ExecContext(ctx, `INSERT INTO uploads(site_id,`+cols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			s.ID, f.ID, f.Name, f.Folder, f.Size, f.Type, f.hash, f.Author, f.AuthorName, f.Created); err != nil {
			return nil, err
		}
		used += size
		count++
		f.URL = fileURL(s, u.ID, f)
		out = append(out, f)
		app.Audit(ctx, "upload.create", s.ID, name, map[string]any{"id": f.ID, "size": size, "type": k.ct})
	}
	if len(out) == 0 {
		return nil, core.BadRequest("choose a file to upload")
	}
	if len(out) == 1 {
		return core.Created{V: out[0]}, nil
	}
	return core.Created{V: map[string]any{"items": out}}, nil
}

func disposition(f *File, inline bool) string {
	d := "attachment"
	if inline {
		d = "inline"
	}
	if v := mime.FormatMediaType(d, map[string]string{"filename": f.Name}); v != "" {
		return v
	}
	return d + `; filename="file` + path.Ext(f.Name) + `"; filename*=UTF-8''` + url.PathEscape(f.Name)
}

// serveFile serves /s/<slug>/~f/<ticket>/<id>/<name>. The ticket (site,
// viewer, expiry) and the viewer's site access were checked by sites.
func serveFile(w http.ResponseWriter, r *http.Request, s *sites.SiteRecord, t sites.Ticket, viewer *core.User, rest string) {
	id, _, _ := strings.Cut(rest, "/")
	notFound := func() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "This file doesn't exist or was removed.")
	}
	if id == "" || len(id) > 64 {
		notFound()
		return
	}
	ctx := r.Context()
	f, err := getFile(ctx, s.ID, id)
	if err != nil {
		notFound()
		return
	}
	role := sites.RoleOf(ctx, viewer, s)
	if !data.AccessFor(viewer, role, Rule(ctx, s.ID)).CanRead(viewer.ID, f.Author) {
		notFound()
		return
	}
	fh, err := app.Blobs.Open(f.hash)
	if err != nil {
		notFound()
		return
	}
	defer fh.Close()
	k, _ := typeFor(f.Name)
	ct := f.Type
	inline := k.inline
	h := w.Header()
	// Uploaded content never runs scripts: a strict sandbox CSP for
	// everything except PDF (browsers refuse to render PDFs in sandboxes).
	if ct == "application/pdf" {
		h.Set("Content-Security-Policy", "default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'self'")
	} else {
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'; sandbox")
	}
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", disposition(f, inline))
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("ETag", `"`+f.hash[:24]+`"`)
	h.Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, "", time.Time{}, fh)
}

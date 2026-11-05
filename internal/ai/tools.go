package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kucukkanat/bunker/internal/core"
	"github.com/kucukkanat/bunker/internal/sites"
)

// Builder limits.
var (
	maxWriteBytes  = 1 << 20 // per file written by the builder
	maxReadBytes   = 100 << 10
	maxSiteFiles   = 300
	maxSearchHits  = 60
	inlineCtxBytes = 48 << 10 // include all files in the first prompt when the site is this small
)

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// builderTools are the tools the builder agent may call.
var builderTools = []Tool{
	{Name: "list_files", Description: "List every file in the site's draft with its size.", Schema: obj(map[string]any{})},
	{Name: "read_file", Description: "Read a text file from the draft. Large files can be read in parts with offset/limit (line numbers, 1-based).",
		Schema: obj(map[string]any{
			"path":   str("File path, e.g. index.html or js/app.js"),
			"offset": map[string]any{"type": "integer", "description": "First line to return (1-based). Optional."},
			"limit":  map[string]any{"type": "integer", "description": "How many lines to return. Optional."},
		}, "path")},
	{Name: "write_file", Description: "Create a file or replace its whole content. Prefer edit_file for changes to an existing file.",
		Schema: obj(map[string]any{"path": str("File path, relative, e.g. index.html"), "content": str("The complete file content")}, "path", "content")},
	{Name: "edit_file", Description: "Replace one exact piece of text in a file. old_text must appear exactly once (include a few surrounding lines to make it unique) unless replace_all is true. Copy old_text exactly from the file, including whitespace.",
		Schema: obj(map[string]any{
			"path":        str("File path"),
			"old_text":    str("Exact text to find"),
			"new_text":    str("Text to put in its place"),
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence. Optional."},
		}, "path", "old_text", "new_text")},
	{Name: "set_collection_rule", Description: "Set who can read and change a bunker.db collection (pages can't do this themselves). Rules: own (default), private, read, append, open, group:<Name>. Takes effect immediately, also before publishing.",
		Schema: obj(map[string]any{"collection": str("Collection name, e.g. requests"), "rule": str("own | private | read | append | open | group:<Name>")}, "collection", "rule")},
	{Name: "delete_file", Description: "Delete a file from the draft.", Schema: obj(map[string]any{"path": str("File path")}, "path")},
	{Name: "search_files", Description: "Find text across all text files. Returns matching lines as path:line: text.",
		Schema: obj(map[string]any{
			"pattern": str("Text to look for (case-insensitive), or a regular expression when regex is true"),
			"regex":   map[string]any{"type": "boolean", "description": "Treat pattern as a regular expression. Optional."},
		}, "pattern")},
}

// workspace applies builder tool calls to a site's draft.
type workspace struct {
	ctx     context.Context
	u       *core.User
	s       *sites.SiteRecord
	changed map[string]string // path → write | delete
	order   []string
	status  func(text string)
	file    func(path, action string, size int)
}

func (w *workspace) manifest() (sites.Manifest, error) {
	m, _, err := sites.WorkingManifest(w.ctx, w.s)
	return m, err
}

func (w *workspace) markChanged(p, action string) {
	if _, ok := w.changed[p]; !ok {
		w.order = append(w.order, p)
	}
	w.changed[p] = action
}

// changedList returns changed paths in order.
func (w *workspace) changedList() []map[string]string {
	out := []map[string]string{}
	for _, p := range w.order {
		out = append(out, map[string]string{"path": p, "action": w.changed[p]})
	}
	return out
}

func isTextFile(p string, b []byte) bool {
	ct := sites.ContentType(p, b)
	if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "javascript") || strings.Contains(ct, "svg") || strings.Contains(ct, "xml") {
		return utf8.Valid(b)
	}
	return utf8.Valid(b) && !bytes.ContainsRune(b, 0)
}

var blockedExt = map[string]bool{".exe": true, ".dll": true, ".msi": true, ".bat": true, ".cmd": true, ".ps1": true, ".scr": true, ".com": true, ".vbs": true, ".jar": true, ".app": true, ".dmg": true, ".pkg": true, ".deb": true, ".rpm": true, ".sh": true}

func cleanToolPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	c, err := sites.CleanPath(p)
	if err != nil {
		return "", errors.New("that path isn't allowed; use a relative path like index.html or css/style.css")
	}
	for _, seg := range strings.Split(c, "/") {
		if strings.HasPrefix(seg, ".") {
			return "", errors.New("hidden files and folders (starting with a dot) aren't allowed")
		}
	}
	if blockedExt[strings.ToLower(path.Ext(c))] {
		return "", errors.New("executable files can't be part of a site")
	}
	return c, nil
}

// externalRefRe finds resources pages can't load (CDNs, web fonts...).
var externalRefRe = regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source|video|audio|embed)\b[^>]*?\s(?:src|href)\s*=\s*["']?(?:https?:)?//([^/"'\s>]+)|@import\s+(?:url\()?\s*["']?(?:https?:)?//([^/"'\s)]+)|\bfetch\(\s*["'` + "`" + `]https?://([^/"'\s` + "`" + `]+)`)

func externalHosts(b []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range externalRefRe.FindAllSubmatch(b, 20) {
		for _, g := range m[1:] {
			if len(g) > 0 && !seen[string(g)] {
				seen[string(g)] = true
				out = append(out, string(g))
			}
		}
	}
	return out
}

func (w *workspace) write(p string, content []byte) (string, error) {
	if len(content) > maxWriteBytes {
		return "", fmt.Errorf("that file is %d KB; the limit is %d KB. Split it into smaller files", len(content)>>10, maxWriteBytes>>10)
	}
	files := map[string][]byte{p: content}
	if err := sites.ScanSecrets(files); err != nil {
		msg := err.Error()
		var he *core.HTTPError
		if errors.As(err, &he) {
			msg = he.Message
		}
		return "", errors.New("blocked: " + msg + " Never put keys, passwords or tokens in files.")
	}
	defer sites.LockDraft(w.s.ID)()
	m, err := w.manifest()
	if err != nil {
		return "", err
	}
	if _, exists := m[p]; !exists && len(m) >= maxSiteFiles {
		return "", fmt.Errorf("the site already has %d files, the most the builder may create", len(m))
	}
	fm, err := sites.StoreFiles(files)
	if err != nil {
		return "", err
	}
	m[p] = fm[p]
	if err := sites.SaveDraft(w.ctx, w.u, w.s, m); err != nil {
		var he *core.HTTPError
		if errors.As(err, &he) {
			return "", errors.New(he.Message)
		}
		return "", err
	}
	w.markChanged(p, "write")
	w.file(p, "write", len(content))
	note := fmt.Sprintf("Saved %s (%d bytes).", p, len(content))
	if hosts := externalHosts(content); len(hosts) > 0 {
		note += " WARNING: it loads resources from " + strings.Join(hosts, ", ") + ", which Bunker pages cannot reach (no network). Use a built-in library from /_/lib/ instead, inline the code, or remove it."
	}
	if strings.HasSuffix(p, ".html") && bytes.Contains(content, []byte("/_/bunker.js")) {
		note += " Note: bunker.js is added automatically; remove the script tag."
	}
	return note, nil
}

func (w *workspace) read(p string) ([]byte, error) {
	m, err := w.manifest()
	if err != nil {
		return nil, err
	}
	f, ok := m[p]
	if !ok {
		return nil, fmt.Errorf("there's no file %s. Use list_files to see what exists", p)
	}
	return app.Blobs.Read(f.Hash)
}

// toolInput decodes and validates tool arguments.
type toolInput struct {
	Path       string  `json:"path"`
	Content    *string `json:"content"`
	OldText    *string `json:"old_text"`
	NewText    *string `json:"new_text"`
	ReplaceAll bool    `json:"replace_all"`
	Offset     int     `json:"offset"`
	Limit      int     `json:"limit"`
	Pattern    string  `json:"pattern"`
	Regex      bool    `json:"regex"`
	Collection string  `json:"collection"`
	Rule       string  `json:"rule"`
	Invalid    bool    `json:"__invalid_json"`
}

// run executes one tool call and returns the text result for the model.
func (w *workspace) run(call ToolCall) (string, error) {
	var in toolInput
	if !validJSONObject(call.Input) {
		return "", errors.New("the tool input wasn't a valid JSON object")
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return "", errors.New("couldn't read the tool input: " + err.Error())
	}
	if in.Invalid {
		return "", errors.New("your tool input was cut off (the reply got too long). Write big files in parts: create the file with the first part, then add the rest with edit_file, or split the app into several files")
	}
	needPath := func() (string, error) {
		if in.Path == "" {
			return "", errors.New("path is required")
		}
		return cleanToolPath(in.Path)
	}
	switch call.Name {
	case "set_collection_rule":
		if in.Collection == "" || in.Rule == "" {
			return "", errors.New("collection and rule are required")
		}
		w.status("Setting who can use " + in.Collection)
		body, _ := json.Marshal(map[string]string{"rule": in.Rule})
		req := httptest.NewRequest("PUT", "/api/v1/sites/"+w.s.Slug+"/db/"+url.PathEscape(in.Collection)+"/_rule", bytes.NewReader(body)).WithContext(w.ctx)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.Mux.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			var e struct {
				Error struct{ Message string } `json:"error"`
			}
			json.Unmarshal(rec.Body.Bytes(), &e)
			return "", errors.New("couldn't set the rule: " + e.Error.Message)
		}
		return "Rule for " + in.Collection + " is now " + in.Rule + ".", nil

	case "list_files":
		w.status("Looking at the files")
		m, err := w.manifest()
		if err != nil {
			return "", err
		}
		if len(m) == 0 {
			return "The draft is empty. Create index.html to start.", nil
		}
		var b strings.Builder
		for _, p := range m.Paths() {
			fmt.Fprintf(&b, "%s (%d bytes)\n", p, m[p].Size)
		}
		return b.String(), nil

	case "read_file":
		p, err := needPath()
		if err != nil {
			return "", err
		}
		w.status("Reading " + p)
		b, err := w.read(p)
		if err != nil {
			return "", err
		}
		if !isTextFile(p, b) {
			return fmt.Sprintf("%s is a binary file (%d bytes); it can't be shown as text.", p, len(b)), nil
		}
		return sliceLines(string(b), in.Offset, in.Limit), nil

	case "write_file":
		p, err := needPath()
		if err != nil {
			return "", err
		}
		if in.Content == nil {
			return "", errors.New("content is required")
		}
		w.status("Writing " + p)
		return w.write(p, []byte(*in.Content))

	case "edit_file":
		p, err := needPath()
		if err != nil {
			return "", err
		}
		if in.OldText == nil || in.NewText == nil {
			return "", errors.New("old_text and new_text are required")
		}
		if *in.OldText == "" {
			return "", errors.New("old_text can't be empty; use write_file to create a file")
		}
		if *in.OldText == *in.NewText {
			return "", errors.New("old_text and new_text are the same, so nothing would change")
		}
		w.status("Editing " + p)
		b, err := w.read(p)
		if err != nil {
			return "", err
		}
		s := string(b)
		n := strings.Count(s, *in.OldText)
		switch {
		case n == 0:
			hint := ""
			if strings.Contains(strings.ToLower(s), strings.ToLower(strings.TrimSpace(*in.OldText))) {
				hint = " (it exists with different spacing or letter case)"
			}
			return "", fmt.Errorf("old_text wasn't found in %s%s. Read the file again and copy the text exactly", p, hint)
		case n > 1 && !in.ReplaceAll:
			return "", fmt.Errorf("old_text appears %d times in %s. Include more surrounding lines so it matches once, or set replace_all", n, p)
		}
		if in.ReplaceAll {
			s = strings.ReplaceAll(s, *in.OldText, *in.NewText)
		} else {
			s = strings.Replace(s, *in.OldText, *in.NewText, 1)
		}
		res, err := w.write(p, []byte(s))
		if err != nil {
			return "", err
		}
		if n > 1 {
			res = fmt.Sprintf("Replaced %d places. ", n) + res
		}
		return res, nil

	case "delete_file":
		p, err := needPath()
		if err != nil {
			return "", err
		}
		w.status("Removing " + p)
		unlock := sites.LockDraft(w.s.ID)
		defer unlock()
		m, err := w.manifest()
		if err != nil {
			return "", err
		}
		if _, ok := m[p]; !ok {
			return "", fmt.Errorf("there's no file %s", p)
		}
		delete(m, p)
		if err := sites.SaveDraft(w.ctx, w.u, w.s, m); err != nil {
			return "", err
		}
		w.markChanged(p, "delete")
		w.file(p, "delete", 0)
		return "Deleted " + p + ".", nil

	case "search_files":
		if strings.TrimSpace(in.Pattern) == "" {
			return "", errors.New("pattern is required")
		}
		if len(in.Pattern) > 500 {
			return "", errors.New("pattern is too long")
		}
		w.status("Searching the files")
		var re *regexp.Regexp
		if in.Regex {
			r, err := regexp.Compile("(?i)" + in.Pattern)
			if err != nil {
				return "", errors.New("that regular expression isn't valid: " + err.Error())
			}
			re = r
		} else {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(in.Pattern))
		}
		m, err := w.manifest()
		if err != nil {
			return "", err
		}
		var hits []string
		for _, p := range m.Paths() {
			if m[p].Size > 2<<20 {
				continue
			}
			b, err := app.Blobs.Read(m[p].Hash)
			if err != nil || !isTextFile(p, b) {
				continue
			}
			for i, line := range strings.Split(string(b), "\n") {
				if re.MatchString(line) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", p, i+1, clip(strings.TrimSpace(line), 200)))
					if len(hits) >= maxSearchHits {
						break
					}
				}
			}
			if len(hits) >= maxSearchHits {
				hits = append(hits, "(more matches not shown)")
				break
			}
		}
		if len(hits) == 0 {
			return "No matches.", nil
		}
		return strings.Join(hits, "\n"), nil
	}
	return "", fmt.Errorf("there's no tool called %q", call.Name)
}

// sliceLines returns lines [offset, offset+limit) (1-based) and caps size.
func sliceLines(s string, offset, limit int) string {
	if offset <= 1 && limit <= 0 && len(s) <= maxReadBytes {
		if s == "" {
			return "(empty file)"
		}
		return s
	}
	lines := strings.SplitAfter(s, "\n")
	start := max(offset, 1) - 1
	if start >= len(lines) {
		return fmt.Sprintf("(the file has only %d lines)", len(lines))
	}
	end := len(lines)
	if limit > 0 {
		end = min(start+limit, len(lines))
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		if b.Len()+len(lines[i]) > maxReadBytes {
			fmt.Fprintf(&b, "\n(stopped at line %d of %d; read more with offset=%d)", i, len(lines), i+1)
			return b.String()
		}
		b.WriteString(lines[i])
	}
	if start > 0 || end < len(lines) {
		fmt.Fprintf(&b, "\n(lines %d-%d of %d)", start+1, end, len(lines))
	}
	return b.String()
}

// siteContext describes the draft for the first prompt of a build:
// a file list, plus every file's content when the site is small.
func siteContext(ctx context.Context, s *sites.SiteRecord) string {
	m, isDraft, err := sites.WorkingManifest(ctx, s)
	var b strings.Builder
	fmt.Fprintf(&b, "<site title=%q address=%q live_version=\"%d\" has_unpublished_draft=\"%t\">\n", s.Title, s.Slug, s.Version, isDraft)
	if err != nil || len(m) == 0 {
		b.WriteString("The site has no files yet.\n</site>")
		return b.String()
	}
	b.WriteString("Files:\n")
	paths := m.Paths()
	var textTotal int64
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s (%d bytes)\n", p, m[p].Size)
		if isTextPath(p) {
			textTotal += m[p].Size
		}
	}
	if textTotal <= int64(inlineCtxBytes) {
		sort.Strings(paths)
		for _, p := range paths {
			if !isTextPath(p) {
				continue
			}
			c, err := app.Blobs.Read(m[p].Hash)
			if err != nil || !utf8.Valid(c) {
				continue
			}
			fmt.Fprintf(&b, "\n<file path=%q>\n%s\n</file>\n", p, c)
		}
		b.WriteString("\n(All text files are shown above, so you don't need to read them again.)\n")
	}
	b.WriteString("</site>")
	return b.String()
}

func isTextPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm", ".js", ".mjs", ".css", ".json", ".txt", ".md", ".svg", ".csv", ".xml", ".webmanifest":
		return true
	}
	return false
}

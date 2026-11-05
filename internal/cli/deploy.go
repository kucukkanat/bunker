package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var isWindows = runtime.GOOS == "windows"

func init() {
	commands = append(commands, &command{
		Name: "deploy", Aliases: []string{"publish", "up"}, Args: "[dir|file|-]", Group: groupStart,
		Summary: "Publish a folder or file (creates the site on first run)",
		Help: `Deploys the current folder (or the given folder or file) as a new version.

The first deploy creates the site and writes bunker.json {server, site}; later
deploys update that site and upload only files that changed. When package.json
has a build script, Bunker builds first (npm, pnpm, yarn or bun) and uploads
dist/, build/ or out/. Files matched by .gitignore or .bunkerignore are skipped.

Examples:
  bunker deploy                       # this folder
  bunker deploy ./site -m "New menu"  # another folder, with a message
  bunker deploy report.html --open    # one file
  cat page.html | bunker deploy -     # from stdin
  bunker deploy --draft               # save as a draft to preview`,
		Flags: []flagDoc{
			{Name: "site", Short: "s", Arg: "slug", Desc: "Deploy to this site (default: bunker.json, or create one)"},
			{Name: "message", Short: "m", Arg: "text", Desc: "Describe the change"},
			{Name: "title", Short: "t", Arg: "name", Desc: "Name for a new site (default: page <title> or folder name)"},
			{Name: "draft", Desc: "Save as a draft instead of publishing"},
			{Name: "open", Short: "o", Desc: "Open the site in your browser afterwards"},
			{Name: "build", Desc: "Always run the build script first"},
			{Name: "no-build", Desc: "Never run the build script"},
			{Name: "dir", Arg: "path", Desc: "Upload this subfolder (e.g. dist)"},
			{Name: "filename", Arg: "name", Desc: "File name for stdin input (default index.html)"},
		},
		Run: cmdDeploy,
	})
}

type deployResult struct {
	Site     string `json:"site"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url"`
	Version  int64  `json:"version,omitempty"`
	Draft    bool   `json:"draft,omitempty"`
	Files    int    `json:"files"`
	Uploaded int    `json:"uploaded"`
	Bytes    int64  `json:"bytes"`
	Created  bool   `json:"created,omitempty"`
	Server   string `json:"server"`
	Ms       int64  `json:"ms"`
	// Functions are the server functions (api/*.js) the server found,
	// when it reports them. Older servers don't.
	Functions []fnInfo `json:"functions,omitempty"`
}

func cmdDeploy(x *App, a *Args) error {
	start := time.Now()
	target := firstNonEmpty(a.Arg(0), ".")
	if len(a.Pos) > 1 {
		return usageErr("deploy takes one folder or file")
	}
	if target == "-" {
		b, err := io.ReadAll(io.LimitReader(x.Stdin, 100<<20))
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return usageErr("nothing on stdin; pipe a page in, e.g. `cat page.html | bunker deploy -`")
		}
		name := a.Str("filename")
		if name == "" {
			name = "index.html"
			if bytes.HasPrefix(b, []byte("PK\x03\x04")) {
				name = "upload.zip"
			}
		}
		return x.deploySingle(a, cwdProject(), name, b, start)
	}
	st, err := os.Stat(target)
	if err != nil {
		return usageErr("%s doesn't exist", target)
	}
	if !st.IsDir() {
		b, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		proj, _ := readProject(filepath.Dir(target))
		if proj == nil {
			proj = &Project{}
		}
		return x.deploySingle(a, proj, filepath.Base(target), b, start)
	}
	root, _ := filepath.Abs(target)
	proj, err := readProject(root)
	if err != nil {
		return err
	}
	c, err := x.client(proj.Server)
	if err != nil {
		return err
	}
	// Build step.
	dir := root
	if sub := firstNonEmpty(a.Str("dir"), proj.Dir); sub != "" {
		dir = filepath.Join(root, sub)
	}
	if !a.Bool("no-build") && a.Str("dir") == "" {
		if plan := detectBuild(root, proj); plan != nil {
			if a.Bool("build") || plan.Out == "" || proj.Build != "" {
				if err := x.runBuild(root, plan); err != nil {
					return err
				}
				plan.Out = findOutput(root, proj)
				if plan.Out == "" {
					return fmt.Errorf("the build finished but no dist/, build/ or out/ folder appeared; set \"dir\" in bunker.json")
				}
			} else {
				x.info("Using the existing %s/ (add --build to rebuild)", plan.Out)
			}
			dir = filepath.Join(root, plan.Out)
		}
	}
	sp := x.spin("Reading files…")
	files, err := collectFiles(dir, NewIgnorer(dir))
	if err != nil {
		sp.end()
		return err
	}
	if len(files) == 0 {
		sp.end()
		return usageErr("there are no files to deploy in %s", dir)
	}
	sp.update(fmt.Sprintf("Hashing %d files…", len(files)))
	if err := hashAll(files, max(4, runtime.NumCPU())); err != nil {
		sp.end()
		return err
	}
	var total int64
	hasIndex := false
	for _, f := range files {
		total += f.Size
		if f.Rel == "index.html" {
			hasIndex = true
		}
	}
	site := a.Str("site")
	if site == "" && (proj.Server == "" || normalizeServer(proj.Server) == c.Server) {
		site = proj.Site
	}
	title := a.Str("title")
	if title == "" && site == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "index.html")); err == nil {
			title = htmlTitle(b)
		}
		if title == "" {
			title = prettyName(filepath.Base(root))
		}
	}
	res := &deployResult{Site: site, Server: c.Server, Files: len(files), Bytes: total, Title: title}
	ctx := x.ctx
	if !hasIndex {
		// No index.html at the root: let the server normalize (single
		// markdown file, wrapped folder...) with a regular upload.
		sp.update("Uploading…")
		if err := x.uploadMultipart(ctx, c, files, site, title, a, res); err != nil {
			sp.end()
			return err
		}
		res.Uploaded = len(files)
	} else {
		sp.update("Checking what changed…")
		hashes := make([]string, 0, len(files))
		seen := map[string]bool{}
		for _, f := range files {
			if !seen[f.Hash] {
				seen[f.Hash] = true
				hashes = append(hashes, f.Hash)
			}
		}
		var miss struct {
			Missing []string `json:"missing"`
		}
		if err := c.Call(ctx, "POST", "/api/v1/blobs/missing", nil, map[string]any{"hashes": hashes}, &miss); err != nil {
			sp.end()
			return err
		}
		need := map[string]bool{}
		for _, h := range miss.Missing {
			need[h] = true
		}
		var up []*localFile
		for _, f := range files {
			if need[f.Hash] {
				up = append(up, f)
				delete(need, f.Hash)
			}
		}
		if err := x.uploadBlobs(ctx, c, up, sp); err != nil {
			sp.end()
			return err
		}
		res.Uploaded = len(up)
		if site == "" {
			sp.update("Creating the site…")
			var s struct{ Slug, Title string }
			if err := c.Call(ctx, "POST", "/api/v1/sites", nil, map[string]any{"title": title}, &s); err != nil {
				sp.end()
				return err
			}
			site, res.Site, res.Created, res.Title = s.Slug, s.Slug, true, s.Title
		}
		sp.update("Publishing…")
		m := map[string]string{}
		for _, f := range files {
			m[f.Rel] = f.Hash
		}
		var out struct {
			Version int64  `json:"version"`
			URL     string `json:"url"`
			Draft     bool     `json:"draft"`
			Functions []fnInfo `json:"functions"`
		}
		body := map[string]any{"files": m, "message": a.Str("message"), "publish": !a.Bool("draft"), "source": "cli"}
		if err := c.Call(ctx, "POST", "/api/v1/sites/"+pathEsc(site)+"/commit", nil, body, &out); err != nil {
			sp.end()
			if res.Created {
				_ = c.Call(context.WithoutCancel(ctx), "DELETE", "/api/v1/sites/"+pathEsc(site), nil, nil, nil)
			}
			if ae, ok := err.(*APIError); ok && ae.Status == 404 && proj.Site == site {
				ae.Message = "the site " + site + " from bunker.json doesn't exist (or you can't edit it)"
				ae.Hint = "Remove \"site\" from bunker.json to create a new one, or pass --site."
			}
			return err
		}
		res.Version, res.Draft, res.Functions = out.Version, out.Draft, out.Functions
		res.URL = absURL(c.Server, firstNonEmpty(out.URL, siteURL(c.Server, site)))
	}
	sp.end()
	if res.Draft {
		res.URL = siteURL(c.Server, res.Site) + "?draft=1"
	}
	// Remember where this folder deploys.
	if proj.Site != res.Site || normalizeServer(proj.Server) != c.Server {
		proj.Site, proj.Server = res.Site, c.Server
		if err := proj.save(); err == nil && !x.JSON {
			x.info("Saved %s (deploys from this folder go to %s)", projectFile, res.Site)
		}
	}
	res.Ms = time.Since(start).Milliseconds()
	return x.finishDeploy(a, res)
}

func (x *App) finishDeploy(a *Args, res *deployResult) error {
	if x.JSON {
		return x.printJSON(res)
	}
	what := fmt.Sprintf("v%d", res.Version)
	if res.Draft {
		what = "draft"
	}
	verb := "Deployed"
	if res.Created {
		verb = "Created and deployed"
	}
	x.ok("%s %s %s %s", verb, x.errSt.bold(res.Site), x.errSt.dim(what),
		x.errSt.dim(fmt.Sprintf("(%s, %d uploaded, %s, %.1fs)", plural(res.Files, "file"), res.Uploaded, humanBytes(res.Bytes), float64(res.Ms)/1000)))
	fmt.Fprintln(x.Stdout, x.outSt.url(res.URL))
	x.printDeployedFunctions(res)
	if isTTY(x.Stdout) && copyToClipboard(res.URL) {
		x.info("Link copied to the clipboard")
	}
	if a.Bool("open") {
		openBrowser(res.URL)
	}
	if res.Created {
		x.info("Only you can open it. Share it: bunker share %s someone@company.com", res.Site)
	}
	return nil
}

func (x *App) runBuild(root string, plan *buildPlan) error {
	run := func(argv []string) error {
		if _, err := exec.LookPath(argv[0]); err != nil {
			return fmt.Errorf("%s isn't installed, but this project needs it to build. Install it or deploy the built folder with --dir", argv[0])
		}
		x.info("Running %s", x.errSt.bold(strings.Join(argv, " ")))
		cmd := exec.CommandContext(x.ctx, argv[0], argv[1:]...)
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = x.Stderr, x.Stderr // keep stdout clean for --json
		cmd.Env = append(os.Environ(), "CI=1")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s failed: %v", strings.Join(argv, " "), err)
		}
		return nil
	}
	if plan.Install != nil {
		if err := run(plan.Install); err != nil {
			return err
		}
	}
	return run(plan.Build)
}

// uploadBlobs PUTs missing content in parallel (bounded).
func (x *App) uploadBlobs(ctx context.Context, c *Client, files []*localFile, sp *spinner) error {
	if len(files) == 0 {
		return nil
	}
	var done atomic.Int64
	var sent atomic.Int64
	var total int64
	for _, f := range files {
		total += f.Size
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan *localFile)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for range min(8, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range ch {
				if ctx.Err() != nil {
					continue
				}
				err := func() error {
					fh, err := os.Open(f.Abs)
					if err != nil {
						return err
					}
					defer fh.Close()
					resp, err := c.Raw(ctx, "PUT", "/api/v1/blobs/"+f.Hash, nil, fh, "application/octet-stream")
					if err != nil {
						return err
					}
					defer resp.Body.Close()
					if resp.StatusCode >= 400 {
						e := c.errorFrom(resp)
						if ae, ok := e.(*APIError); ok {
							ae.Message = f.Rel + ": " + ae.Message
						}
						return e
					}
					io.Copy(io.Discard, resp.Body)
					return nil
				}()
				if err != nil {
					once.Do(func() { first = err; cancel() })
					continue
				}
				n := done.Add(1)
				s := sent.Add(f.Size)
				sp.update(fmt.Sprintf("Uploading %d/%d files (%s of %s)…", n, len(files), humanBytes(s), humanBytes(total)))
			}
		}()
	}
	for _, f := range files {
		ch <- f
	}
	close(ch)
	wg.Wait()
	return first
}

// uploadMultipart sends files as a regular multipart deploy (or drop).
func (x *App) uploadMultipart(ctx context.Context, c *Client, files []*localFile, site, title string, a *Args, res *deployResult) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			if m := a.Str("message"); m != "" {
				mw.WriteField("message", m)
			}
			if a.Bool("draft") {
				mw.WriteField("publish", "false")
			}
			mw.WriteField("source", "cli")
			for _, f := range files {
				w, err := mw.CreateFormFile("file:"+f.Rel, filepath.Base(f.Rel))
				if err != nil {
					return err
				}
				fh, err := os.Open(f.Abs)
				if err != nil {
					return err
				}
				_, err = io.Copy(w, fh)
				fh.Close()
				if err != nil {
					return err
				}
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	return x.postUpload(ctx, c, pr, mw.FormDataContentType(), site, title, a, res)
}

func (x *App) postUpload(ctx context.Context, c *Client, body io.Reader, ct, site, title string, a *Args, res *deployResult) error {
	q := url.Values{}
	p := "/api/v1/sites/" + pathEsc(site) + "/deploy"
	if site == "" {
		p = "/api/v1/drop"
		if title != "" {
			q.Set("title", title)
		}
		q.Set("source", "cli")
	}
	var out struct {
		Slug    string `json:"slug"`
		Title   string `json:"title"`
		Version int64  `json:"version"`
		URL     string `json:"url"`
		Draft     bool     `json:"draft"`
		Functions []fnInfo `json:"functions"`
	}
	resp, err := c.Raw(ctx, "POST", p, q, body, ct)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.errorFrom(resp)
	}
	if err := jsonDecode(resp.Body, &out); err != nil {
		return err
	}
	if site == "" {
		res.Site, res.Created, res.Title = out.Slug, true, out.Title
		if a.Bool("draft") {
			x.warn("New sites are published right away; --draft applies from the next deploy.")
		}
	}
	res.Version, res.Draft, res.Functions = out.Version, out.Draft, out.Functions
	res.URL = absURL(c.Server, firstNonEmpty(out.URL, siteURL(c.Server, res.Site)))
	return nil
}

func (x *App) deploySingle(a *Args, proj *Project, name string, data []byte, start time.Time) error {
	c, err := x.client(proj.Server)
	if err != nil {
		return err
	}
	site := a.Str("site")
	if site == "" && (proj.Server == "" || normalizeServer(proj.Server) == c.Server) {
		site = proj.Site
	}
	title := a.Str("title")
	if title == "" && site == "" {
		title = htmlTitle(data)
		if title == "" && name != "index.html" {
			title = prettyName(name)
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if m := a.Str("message"); m != "" {
		mw.WriteField("message", m)
	}
	if a.Bool("draft") {
		mw.WriteField("publish", "false")
	}
	mw.WriteField("source", "cli")
	w, _ := mw.CreateFormFile("files", name)
	w.Write(data)
	mw.Close()
	res := &deployResult{Site: site, Server: c.Server, Files: 1, Uploaded: 1, Bytes: int64(len(data)), Title: title}
	sp := x.spin("Uploading " + name + "…")
	err = x.postUpload(x.ctx, c, &buf, mw.FormDataContentType(), site, title, a, res)
	sp.end()
	if err != nil {
		return err
	}
	if res.Draft {
		res.URL = siteURL(c.Server, res.Site) + "?draft=1"
	}
	res.Ms = time.Since(start).Milliseconds()
	if err := x.finishDeploy(a, res); err != nil {
		return err
	}
	if res.Created && !x.JSON {
		// Single files and stdin don't write bunker.json (a folder can hold
		// many of them), so without --site the next deploy makes another app.
		x.info("To update this app next time: bunker deploy %s --site %s", firstNonEmpty(a.Arg(0), name), res.Site)
	}
	return nil
}

// printDeployedFunctions lists the server functions in a deploy, on
// stderr so stdout stays the URL.
func (x *App) printDeployedFunctions(res *deployResult) {
	if len(res.Functions) == 0 {
		return
	}
	x.info("%s:", plural(len(res.Functions), "server function"))
	broken := false
	for _, f := range res.Functions {
		line := "  " + x.errSt.bold(f.Name)
		// Deploy replies list name and path; triggers only when the
		// server includes them (see `bunker fn ls` for the full picture).
		var notes []string
		if f.Path != "" {
			notes = append(notes, f.Path)
		}
		if f.Elevated {
			notes = append(notes, "runs as the owner")
		}
		if f.Schedule != "" || len(f.OnWrite) > 0 {
			notes = append(notes, f.triggers())
		}
		if len(notes) > 0 {
			line += " " + x.errSt.dim("("+strings.Join(notes, "; ")+")")
		}
		if f.Error != "" {
			broken = true
			line += " " + x.errSt.red(f.Error)
		}
		fmt.Fprintln(x.Stderr, line)
	}
	if broken {
		x.warn("Some functions have errors and won't run until they're fixed.")
	}
}

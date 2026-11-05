package sites

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

// GitHub import: paste https://github.com/owner/repo (optionally
// /tree/<branch>/<folder>) and get a site. The server only ever talks to
// codeload.github.com (a fixed host), so this adds no SSRF surface.

var ghRe = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?(?:/tree/([^/]+)(/.*)?)?/?$`)

// GitHubSource parses a GitHub URL into owner, repo, ref and folder.
func GitHubSource(u string) (owner, repo, ref, dir string, ok bool) {
	m := ghRe.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return "", "", "", "", false
	}
	return m[1], m[2], m[3], strings.Trim(m[4], "/"), true
}

var ghClient = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if req.URL.Host != "codeload.github.com" || len(via) > 3 {
		return http.ErrUseLastResponse
	}
	return nil
}}

// CodeloadBase is overridable in tests.
var CodeloadBase = "https://codeload.github.com"

func fetchGitHub(ctx context.Context, owner, repo, ref, dir string) (map[string][]byte, error) {
	if ref == "" {
		ref = "HEAD"
	}
	src := CodeloadBase + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/zip/" + url.PathEscape(ref)
	req, _ := http.NewRequestWithContext(ctx, "GET", src, nil)
	res, err := ghClient.Do(req)
	if err != nil {
		return nil, core.Unavailable("couldn't reach GitHub")
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil, core.NotFound("that repository or branch wasn't found (private repositories can't be imported; use the CLI instead)")
	}
	if res.StatusCode != 200 {
		return nil, core.Unavailable("GitHub answered " + res.Status)
	}
	max := int64(core.EnvInt("UPLOAD_MAX_MB", 100)) << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, core.Unavailable("download from GitHub failed")
	}
	if int64(len(data)) > max {
		return nil, core.TooLarge("that repository is larger than the upload limit")
	}
	files := map[string][]byte{}
	if err := addUploaded(files, "repo.zip", data); err != nil {
		return nil, err
	}
	// Strip the "repo-branch/" wrapper and keep only the chosen folder.
	out := map[string][]byte{}
	for p, b := range files {
		i := strings.Index(p, "/")
		if i < 0 {
			continue
		}
		rel := p[i+1:]
		if dir != "" {
			if !strings.HasPrefix(rel, dir+"/") {
				continue
			}
			rel = strings.TrimPrefix(rel, dir+"/")
		}
		if rel != "" {
			out[rel] = b
		}
	}
	if len(out) == 0 {
		return nil, core.BadRequest("nothing to deploy in that folder")
	}
	return out, nil
}

func registerImport(a *core.App) {
	a.Handle(core.Op{Name: "sites.import_github", Method: "POST", Path: "/api/v1/import/github", Summary: "Create a site from a public GitHub repository (or one folder of it)",
		Params: []core.Param{{Name: "url", In: "body", Required: true, Desc: "https://github.com/owner/repo or .../tree/branch/folder"},
			{Name: "title", In: "body"}, {Name: "site", In: "body", Desc: "Update this existing site instead of creating one"}},
		Fn: func(r *http.Request) (any, error) {
			if a.Setting("import.github") == "false" {
				return nil, core.Forbidden("importing from GitHub is turned off on this server")
			}
			u := core.MustUser(r)
			var in struct{ URL, Title, Site string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			owner, repo, ref, dir, ok := GitHubSource(in.URL)
			if !ok {
				return nil, core.BadRequest("paste a link like https://github.com/owner/repo")
			}
			files, err := fetchGitHub(r.Context(), owner, repo, ref, dir)
			if err != nil {
				return nil, err
			}
			msg := "Imported from github.com/" + owner + "/" + repo
			if in.Site != "" {
				s, _, err := Full(r.Context(), u, in.Site, core.RoleEditor)
				if err != nil {
					return nil, err
				}
				res, err := deployFiles(r.Context(), u, s, files, msg, "github", true)
				if err == nil {
					res["slug"] = s.Slug
				}
				return res, err
			}
			title := in.Title
			if title == "" {
				title = repo
				if dir != "" {
					title = dir[strings.LastIndex(dir, "/")+1:]
				}
				title = strings.ReplaceAll(strings.ReplaceAll(title, "-", " "), "_", " ")
			}
			s, err := CreateSite(r.Context(), u, title, "", "From github.com/"+owner+"/"+repo, "")
			if err != nil {
				return nil, err
			}
			res, err := deployFiles(r.Context(), u, s, files, msg, "github", true)
			if err != nil {
				a.DB.ExecContext(r.Context(), `DELETE FROM sites WHERE id=?`, s.ID)
				return nil, err
			}
			res["slug"], res["title"] = s.Slug, s.Title
			a.Audit(r.Context(), "site.import", s.ID, "github.com/"+owner+"/"+repo, map[string]any{"ref": ref, "dir": dir})
			return core.Created{V: res}, nil
		}})
}

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func init() {
	commands = append(commands,
		&command{
			Name: "open", Args: "[site]", Group: groupStart,
			Summary: "Open a site (or the dashboard) in your browser",
			Flags:   []flagDoc{{Name: "draft", Desc: "Open the draft preview"}},
			Run:     cmdOpen,
		},
		&command{
			Name: "sites", Aliases: []string{"site", "ls"}, Args: "[ls|create|rm|rename|info] ...", Group: groupSites,
			Summary: "List, create, rename and remove sites",
			Help: `  bunker sites                         list sites you can open
  bunker sites ls --filter mine         mine | shared | org | starred | recent | templates | trash
  bunker sites create "Team Lunch"      create an empty site
  bunker sites info team-lunch          details and who has access
  bunker sites rename team-lunch "Friday Lunch" [--slug friday-lunch]
  bunker sites rm team-lunch            move to the trash (restorable for 30 days)`,
			Flags: []flagDoc{
				{Name: "filter", Short: "f", Arg: "which", Desc: "ls: all, mine, shared, org, starred, recent, templates, trash"},
				{Name: "query", Short: "q", Arg: "text", Desc: "ls: search titles"},
				{Name: "slug", Arg: "address", Desc: "create/rename: the site's address"},
				{Name: "description", Arg: "text", Desc: "create: a short description"},
			},
			Run: cmdSites,
		},
		&command{
			Name: "share", Args: "[site] <email|group>...", Group: groupSites,
			Summary: "Share a site with people or groups",
			Help: `Anything with an @ is a person; anything else is a group name or ID.

  bunker share team-lunch ada@corp.com
  bunker share team-lunch "London Office" --role editor
  bunker share ada@corp.com            # site from bunker.json`,
			Flags: []flagDoc{{Name: "role", Short: "r", Arg: "role", Desc: "viewer (default) or editor"}},
			Run:   cmdShare,
		},
		&command{
			Name: "unshare", Args: "[site] <email|group>...", Group: groupSites,
			Summary: "Stop sharing a site with people or groups",
			Run:     cmdUnshare,
		},
		&command{
			Name: "versions", Aliases: []string{"history"}, Args: "[site]", Group: groupSites,
			Summary: "List a site's versions",
			Run:     cmdVersions,
		},
		&command{
			Name: "rollback", Args: "[site] [version]", Group: groupSites,
			Summary: "Make an earlier version live again (default: the previous one)",
			Run:     cmdRollback,
		},
		&command{
			Name: "pull", Args: "<site> [dir]", Group: groupSites,
			Summary: "Download a site's working files into a folder",
			Flags: []flagDoc{
				{Name: "version", Short: "v", Arg: "n", Desc: "Download this version instead of the working copy"},
				{Name: "force", Desc: "Overwrite files in a non-empty folder"},
			},
			Run: cmdPull,
		},
	)
}

// siteFrom resolves the site from args[i] or bunker.json.
func siteFrom(a *Args, i int, example string) (string, error) {
	if s := a.Arg(i); s != "" {
		return s, nil
	}
	if p := cwdProject(); p.Site != "" {
		return p.Site, nil
	}
	return "", usageErr("which site? pass its address, e.g. `%s` (or run it in a folder with bunker.json)", example)
}

type siteInfo struct {
	ID          int64                         `json:"id"`
	Slug        string                        `json:"slug"`
	Title       string                        `json:"title"`
	Version     int64                         `json:"version"`
	Role        string                        `json:"role"`
	URL         string                        `json:"url"`
	Icon        string                        `json:"icon"`
	Description string                        `json:"description"`
	UpdatedAt   int64                         `json:"updatedAt"`
	OrgView     bool                          `json:"orgView"`
	Owner       *struct{ Name, Email string } `json:"owner"`
	Extra       map[string]any                `json:"extra"`
}

func cmdOpen(x *App, a *Args) error {
	proj := cwdProject()
	server, _, err := x.conn(proj.Server)
	if err != nil && server == "" {
		return err
	}
	u := server + "/"
	if s := firstNonEmpty(a.Arg(0), proj.Site); s != "" {
		u = siteURL(server, s)
		if a.Bool("draft") {
			u += "?draft=1"
		}
	}
	return x.result(map[string]any{"url": u}, func() {
		if openBrowser(u) {
			x.ok("Opened %s", x.errSt.url(u))
		} else {
			fmt.Fprintln(x.Stdout, u)
		}
	})
}

func cmdSites(x *App, a *Args) error {
	sub := a.Arg(0)
	args := a.Pos
	if len(args) > 0 {
		args = args[1:]
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	switch sub {
	case "", "ls", "list":
		q := url.Values{}
		if f := a.Str("filter"); f != "" {
			q.Set("filter", f)
		}
		if s := a.Str("query"); s != "" {
			q.Set("q", s)
		}
		var l struct {
			Sites []siteInfo `json:"sites"`
		}
		if err := c.Get(x.ctx, "/api/v1/sites", q, &l); err != nil {
			return err
		}
		return x.result(l, func() {
			if len(l.Sites) == 0 {
				x.info("No sites yet. Create one with %s", x.errSt.bold("bunker deploy"))
				return
			}
			var rows [][]string
			for _, s := range l.Sites {
				v := "-"
				if s.Version > 0 {
					v = "v" + strconv.FormatInt(s.Version, 10)
				}
				rows = append(rows, []string{x.outSt.bold(s.Slug), clipStr(s.Title, 40), s.Role, v, relTime(s.UpdatedAt)})
			}
			x.table([]string{"SITE", "TITLE", "ROLE", "LIVE", "UPDATED"}, rows)
		})
	case "create", "new":
		title := strings.Join(args, " ")
		if title == "" {
			return usageErr("give the new site a name, e.g. `bunker sites create \"Team Lunch\"`")
		}
		var s siteInfo
		if err := c.Call(x.ctx, "POST", "/api/v1/sites", nil, map[string]any{"title": title, "slug": a.Str("slug"), "description": a.Str("description")}, &s); err != nil {
			return err
		}
		return x.result(s, func() {
			x.ok("Created %s", x.errSt.bold(s.Slug))
			fmt.Fprintln(x.Stdout, x.outSt.url(siteURL(c.Server, s.Slug)))
			x.info("Deploy files to it: bunker deploy --site %s", s.Slug)
		})
	case "info", "get", "show":
		if len(args) == 0 {
			if p := cwdProject(); p.Site != "" {
				args = []string{p.Site}
			} else {
				return usageErr("which site? e.g. `bunker sites info team-lunch`")
			}
		}
		var s siteInfo
		if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(args[0]), nil, &s); err != nil {
			return err
		}
		return x.result(s, func() {
			st := x.outSt
			fmt.Fprintf(x.Stdout, "%s %s\n", st.bold(strings.TrimSpace(s.Icon+" "+s.Title)), st.dim("("+s.Slug+")"))
			fmt.Fprintln(x.Stdout, st.url(siteURL(c.Server, s.Slug)))
			if s.Description != "" {
				fmt.Fprintln(x.Stdout, s.Description)
			}
			fmt.Fprintf(x.Stdout, "%s v%d   %s %s   %s %s\n", st.dim("live"), s.Version, st.dim("your role"), s.Role, st.dim("updated"), relTime(s.UpdatedAt))
			if s.Owner != nil {
				fmt.Fprintf(x.Stdout, "%s %s\n", st.dim("owner"), firstNonEmpty(s.Owner.Name, s.Owner.Email))
			}
			if s.OrgView {
				fmt.Fprintln(x.Stdout, st.dim("everyone in the organization can view"))
			}
			if ms, ok := s.Extra["members"].([]any); ok && len(ms) > 0 {
				fmt.Fprintln(x.Stdout, st.dim("shared with"))
				for _, m := range ms {
					mm, _ := m.(map[string]any)
					fmt.Fprintf(x.Stdout, "  %s %s %s\n", firstNonEmpty(fmt.Sprint(mm["name"]), fmt.Sprint(mm["id"])), st.dim(fmt.Sprint(mm["type"])), fmt.Sprint(mm["role"]))
				}
			}
		})
	case "rm", "delete", "remove":
		if len(args) == 0 {
			return usageErr("which site? e.g. `bunker sites rm team-lunch`")
		}
		for _, slug := range args {
			if err := x.confirm(fmt.Sprintf("Move %s to the trash?", x.errSt.bold(slug))); err != nil {
				return err
			}
			if err := c.Call(x.ctx, "DELETE", "/api/v1/sites/"+pathEsc(slug), nil, nil, nil); err != nil {
				return err
			}
			x.ok("Moved %s to the trash (restore it within 30 days from the dashboard)", slug)
		}
		if x.JSON {
			return x.printJSON(map[string]any{"ok": true, "deleted": args})
		}
		return nil
	case "rename", "mv":
		if len(args) < 1 || (len(args) < 2 && a.Str("slug") == "") {
			return usageErr("e.g. `bunker sites rename team-lunch \"Friday Lunch\" [--slug friday-lunch]`")
		}
		body := map[string]any{}
		if len(args) > 1 {
			body["title"] = strings.Join(args[1:], " ")
		}
		if s := a.Str("slug"); s != "" {
			body["slug"] = s
		}
		var s siteInfo
		if err := c.Call(x.ctx, "PATCH", "/api/v1/sites/"+pathEsc(args[0]), nil, body, &s); err != nil {
			return err
		}
		if p := cwdProject(); p.Site == args[0] && s.Slug != "" && s.Slug != args[0] {
			p.Site = s.Slug
			p.save()
		}
		return x.result(s, func() {
			x.ok("Renamed to %s %s", x.errSt.bold(firstNonEmpty(s.Title, args[0])), x.errSt.dim("("+firstNonEmpty(s.Slug, args[0])+")"))
		})
	}
	return usageErr("unknown sites command %q (use ls, create, info, rename or rm)", sub)
}

func memberTarget(who string) (typ, id string) {
	if strings.Contains(who, "@") {
		return "user", strings.ToLower(strings.TrimSpace(who))
	}
	return "group", strings.TrimSpace(who)
}

func splitSiteAndTargets(a *Args, example string) (string, []string, error) {
	switch len(a.Pos) {
	case 0:
		return "", nil, usageErr("who? e.g. `%s`", example)
	case 1:
		p := cwdProject()
		if p.Site == "" {
			return "", nil, usageErr("which site? e.g. `%s`", example)
		}
		return p.Site, a.Pos, nil
	}
	return a.Pos[0], a.Pos[1:], nil
}

func cmdShare(x *App, a *Args) error {
	site, who, err := splitSiteAndTargets(a, "bunker share team-lunch ada@corp.com")
	if err != nil {
		return err
	}
	role := firstNonEmpty(a.Str("role"), "viewer")
	if role != "viewer" && role != "editor" {
		return usageErr("--role must be viewer or editor")
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	var last any
	for _, w := range who {
		typ, id := memberTarget(w)
		var out map[string]any
		if err := c.Call(x.ctx, "POST", "/api/v1/sites/"+pathEsc(site)+"/members", nil, map[string]any{"type": typ, "id": id, "role": role}, &out); err != nil {
			return err
		}
		last = out
		x.ok("Shared %s with %s as %s", x.errSt.bold(site), x.errSt.bold(w), role)
	}
	if x.JSON {
		return x.printJSON(last)
	}
	x.info("They can open %s", x.errSt.url(siteURL(c.Server, site)))
	return nil
}

type member struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func cmdUnshare(x *App, a *Args) error {
	site, who, err := splitSiteAndTargets(a, "bunker unshare team-lunch ada@corp.com")
	if err != nil {
		return err
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	var l struct {
		Members []member `json:"members"`
	}
	if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(site)+"/members", nil, &l); err != nil {
		return err
	}
	var removed []string
	for _, w := range who {
		typ, id := memberTarget(w)
		var m *member
		for i := range l.Members {
			mm := &l.Members[i]
			if mm.Type == typ && (strings.EqualFold(mm.ID, id) || strings.EqualFold(mm.Name, id)) {
				m = mm
				break
			}
		}
		if m == nil {
			return fmt.Errorf("%s isn't shared with %s", site, w)
		}
		if err := c.Call(x.ctx, "DELETE", "/api/v1/sites/"+pathEsc(site)+"/members/"+pathEsc(m.Type)+"/"+pathEsc(m.ID), nil, nil, nil); err != nil {
			return err
		}
		removed = append(removed, w)
		x.ok("%s no longer has access to %s", x.errSt.bold(w), site)
	}
	if x.JSON {
		return x.printJSON(map[string]any{"ok": true, "removed": removed})
	}
	return nil
}

type version struct {
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

type versionList struct {
	Live     int64     `json:"live"`
	Versions []version `json:"versions"`
}

func cmdVersions(x *App, a *Args) error {
	site, err := siteFrom(a, 0, "bunker versions team-lunch")
	if err != nil {
		return err
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	var l versionList
	if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(site)+"/versions", nil, &l); err != nil {
		return err
	}
	return x.result(l, func() {
		if len(l.Versions) == 0 {
			x.info("%s has no versions yet. Publish one with bunker deploy", site)
			return
		}
		var rows [][]string
		for _, v := range l.Versions {
			n := "v" + strconv.FormatInt(v.N, 10)
			live := ""
			if v.Live {
				n = x.outSt.green(n)
				live = x.outSt.green("● live")
			}
			rows = append(rows, []string{n, live, relTime(v.CreatedAt), firstNonEmpty(v.Author, v.Email, "-"), v.Source, clipStr(v.Message, 50)})
		}
		x.table([]string{"VERSION", "", "WHEN", "BY", "VIA", "MESSAGE"}, rows)
	})
}

func cmdRollback(x *App, a *Args) error {
	var site, nStr string
	switch len(a.Pos) {
	case 0:
		site = cwdProject().Site
	case 1:
		if _, err := strconv.Atoi(strings.TrimPrefix(a.Pos[0], "v")); err == nil && cwdProject().Site != "" {
			site, nStr = cwdProject().Site, a.Pos[0]
		} else {
			site = a.Pos[0]
		}
	default:
		site, nStr = a.Pos[0], a.Pos[1]
	}
	if site == "" {
		return usageErr("which site? e.g. `bunker rollback team-lunch 3`")
	}
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	var l versionList
	if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(site)+"/versions", nil, &l); err != nil {
		return err
	}
	var n int64
	if nStr != "" {
		v, err := strconv.ParseInt(strings.TrimPrefix(nStr, "v"), 10, 64)
		if err != nil {
			return usageErr("version must be a number, e.g. 3")
		}
		n = v
	} else {
		for _, v := range l.Versions {
			if v.N < l.Live && v.N > n {
				n = v.N
			}
		}
		if n == 0 {
			return fmt.Errorf("%s has no earlier version to roll back to", site)
		}
	}
	if n == l.Live {
		return fmt.Errorf("version %d is already live", n)
	}
	if err := x.confirm(fmt.Sprintf("Make version %d of %s live again?", n, x.errSt.bold(site))); err != nil {
		return err
	}
	var out struct {
		Version int64 `json:"version"`
	}
	if err := c.Call(x.ctx, "POST", fmt.Sprintf("/api/v1/sites/%s/versions/%d/restore", pathEsc(site), n), nil, map[string]any{}, &out); err != nil {
		return err
	}
	return x.result(map[string]any{"site": site, "restored": n, "version": out.Version, "url": siteURL(c.Server, site)}, func() {
		x.ok("%s is back to version %d %s", x.errSt.bold(site), n, x.errSt.dim(fmt.Sprintf("(published as v%d)", out.Version)))
		fmt.Fprintln(x.Stdout, x.outSt.url(siteURL(c.Server, site)))
	})
}

func cmdPull(x *App, a *Args) error {
	site, err := siteFrom(a, 0, "bunker pull team-lunch")
	if err != nil {
		return err
	}
	dir := firstNonEmpty(a.Arg(1), site)
	c, err := x.client(cwdProject().Server)
	if err != nil {
		return err
	}
	q := url.Values{}
	if v := a.Str("version"); v != "" {
		q.Set("version", v)
	}
	var l struct {
		Draft bool `json:"draft"`
		Files []struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
			Hash string `json:"hash"`
		} `json:"files"`
	}
	if err := c.Get(x.ctx, "/api/v1/sites/"+pathEsc(site)+"/files", q, &l); err != nil {
		return err
	}
	if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 && !a.Bool("force") {
		onlyProj := len(ents) == 1 && ents[0].Name() == projectFile
		if !onlyProj {
			return usageErr("%s isn't empty; pass --force to overwrite files there", dir)
		}
	}
	root, _ := filepath.Abs(dir)
	sp := x.spin(fmt.Sprintf("Downloading %d files…", len(l.Files)))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	var total int64
	sem := make(chan struct{}, 8)
	for _, f := range l.Files {
		target := filepath.Join(root, filepath.FromSlash(f.Path))
		if rel, err := filepath.Rel(root, target); err != nil || strings.HasPrefix(rel, "..") {
			continue // never write outside the folder
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			err := func() error {
				resp, err := c.Raw(x.ctx, "GET", "/api/v1/sites/"+pathEsc(site)+"/files/"+escSegments(f.Path), q, nil, "")
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode >= 400 {
					return c.errorFrom(resp)
				}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				if f.Hash != "" {
					sum := sha256.Sum256(b)
					if hex.EncodeToString(sum[:]) != f.Hash {
						return fmt.Errorf("%s: download was corrupted; try again", f.Path)
					}
				}
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				mu.Lock()
				total += int64(len(b))
				mu.Unlock()
				return os.WriteFile(target, b, 0o644)
			}()
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sp.end()
	if first != nil {
		return first
	}
	proj, _ := readProject(root)
	if proj != nil {
		proj.Server, proj.Site = c.Server, site
		proj.save()
	}
	what := "live version"
	if l.Draft {
		what = "draft"
	}
	if v := a.Str("version"); v != "" {
		what = "version " + v
	}
	return x.result(map[string]any{"site": site, "dir": root, "files": len(l.Files), "bytes": total, "draft": l.Draft}, func() {
		x.ok("Downloaded %s's %s: %d files (%s) into %s", x.errSt.bold(site), what, len(l.Files), humanBytes(total), dir)
		x.info("Edit, then run: cd %s && bunker deploy", dir)
	})
}

func escSegments(p string) string {
	segs := strings.Split(p, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return strings.Join(segs, "/")
}

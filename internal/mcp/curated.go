package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

func obj(props map[string]any, req ...string) map[string]any {
	if req == nil {
		req = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var filesSchema = map[string]any{
	"type":                 "object",
	"description":          "Map of file path → text content, or → {\"base64\":\"...\"} for binary files. index.html is required.",
	"additionalProperties": map[string]any{},
}

func ann(title string, ro, destructive, idem bool) map[string]any {
	return map[string]any{"title": title, "readOnlyHint": ro, "destructiveHint": destructive, "idempotentHint": idem, "openWorldHint": false}
}

func curatedTools() []*tool {
	return []*tool{
		{
			Name: "create_site_from_html", Title: "Publish an HTML page as a new site",
			Description: "Create a new private Bunker site from one self-contained HTML document and publish it. Returns the site's slug and URL. Inline your CSS and JS; pages can't load CDN scripts or fetch outside URLs. Use window.bunker (see get_sdk_docs) for data.",
			InputSchema: obj(map[string]any{
				"title": str("Name of the site"),
				"html":  str("The complete index.html"),
				"slug":  str("Optional address (lowercase-with-dashes)"),
			}, "title", "html"),
			Annotations: ann("Publish an HTML page as a new site", false, false, false),
			fn: func(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
				html, _ := a["html"].(string)
				if strings.TrimSpace(html) == "" {
					return errResult("html is required"), nil
				}
				return s.drop(ctx, a, map[string]any{"index.html": html})
			},
		},
		{
			Name: "deploy_files", Title: "Deploy files to a site",
			Description: "Publish files as a new version of an existing site (give `site`), or create a new site (give `title`, omit `site`). Set draft=true to save a draft instead of publishing (preview at /s/<site>/?draft=1).",
			InputSchema: obj(map[string]any{
				"site":    str("Existing site slug. Omit to create a new site."),
				"title":   str("Name for a new site (when site is omitted)"),
				"files":   filesSchema,
				"message": str("What changed"),
				"draft":   map[string]any{"type": "boolean", "description": "Save as draft instead of publishing"},
			}, "files"),
			Annotations: ann("Deploy files to a site", false, false, false),
			fn: func(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
				files, ok := a["files"].(map[string]any)
				if !ok || len(files) == 0 {
					return errResult("files must be a non-empty object of path → content"), nil
				}
				site, _ := a["site"].(string)
				if site == "" {
					return s.drop(ctx, a, files)
				}
				draft, _ := a["draft"].(bool)
				body := map[string]any{"files": files, "message": a["message"], "publish": !draft}
				return s.json(ctx, "POST", "/api/v1/sites/"+url.PathEscape(site)+"/deploy", nil, body)
			},
		},
		{
			Name: "edit_file", Title: "Edit one file of a site",
			Description: "Replace an exact snippet in one file of a site's working copy (draft). The `find` text must occur exactly once. Set publish=true to publish the draft right away.",
			InputSchema: obj(map[string]any{
				"site":    str("Site slug"),
				"path":    str("File path, e.g. index.html"),
				"find":    str("Exact text to replace (must be unique in the file). Empty with `replace` set: replace the whole file."),
				"replace": str("Replacement text"),
				"publish": map[string]any{"type": "boolean", "description": "Publish the draft after editing"},
				"message": str("What changed (when publishing)"),
			}, "site", "path", "replace"),
			Annotations: ann("Edit one file of a site", false, false, false),
			fn:          editFile,
		},
		{
			Name: "get_sdk_docs", Title: "Bunker SDK and deploy docs",
			Description: "Read this before writing a Bunker page: the sandbox rules, the window.bunker SDK (db, files, ai, storage, me, m365), and how to deploy.",
			InputSchema: obj(map[string]any{
				"topic": map[string]any{"type": "string", "enum": []string{"all", "sdk", "sandbox", "deploy", "ops"}, "description": "Which part (default all)"},
			}),
			Annotations: ann("Bunker SDK and deploy docs", true, false, true),
			fn: func(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
				base := s.Backend.BaseURL()
				topic, _ := a["topic"].(string)
				switch topic {
				case "sdk":
					return textResult(SDKReference), nil
				case "sandbox":
					return textResult(SandboxRules), nil
				case "deploy":
					return textResult(DeployGuide(base)), nil
				case "ops":
					ops, err := s.Backend.Ops(ctx)
					if err != nil {
						return nil, err
					}
					return textResult(OpsList(ops, true)), nil
				}
				return textResult("# Bunker\n\n" + Intro + "\n\n" + SandboxRules + "\n\n" + SDKReference + "\n\n" + DeployGuide(base)), nil
			},
		},
		{
			Name: "build_with_ai", Title: "Build or change a site with Bunker's AI",
			Description: "Ask Bunker's built-in AI builder to create a new site from a description (omit `site`) or to change an existing site (give `site`). Changes land in the draft. Needs AI to be configured by the admin.",
			InputSchema: obj(map[string]any{
				"site":   str("Existing site slug to change. Omit to create a new site."),
				"prompt": str("What to build or change, in plain words"),
			}, "prompt"),
			Annotations: ann("Build or change a site with Bunker's AI", false, false, false),
			fn: func(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
				prompt, _ := a["prompt"].(string)
				if strings.TrimSpace(prompt) == "" {
					return errResult("prompt is required"), nil
				}
				if site, _ := a["site"].(string); site != "" {
					return s.json(ctx, "POST", "/api/v1/sites/"+url.PathEscape(site)+"/builder", nil, map[string]any{"message": prompt})
				}
				res, err := s.json(ctx, "POST", "/api/v1/ai/create", nil, map[string]any{"prompt": prompt})
				if err == nil && !res.IsError {
					if m, ok := res.StructuredContent.(map[string]any); ok {
						if slug, _ := m["slug"].(string); slug != "" && m["url"] == nil {
							m["url"] = s.Backend.BaseURL() + "/s/" + slug + "/"
						}
					}
				}
				return res, err
			},
		},
	}
}

func (s *Server) json(ctx context.Context, method, path string, q url.Values, body any) (*toolResult, error) {
	b, err := Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := s.Backend.Do(ctx, &Request{Method: method, Path: path, Query: q, Body: b, ContentType: "application/json"})
	if err != nil {
		return nil, err
	}
	return ResultFrom(resp), nil
}

func (s *Server) drop(ctx context.Context, a map[string]any, files map[string]any) (*toolResult, error) {
	q := url.Values{}
	if t, _ := a["title"].(string); t != "" {
		q.Set("title", t)
	}
	if sl, _ := a["slug"].(string); sl != "" {
		q.Set("slug", sl)
	}
	q.Set("source", "mcp")
	return s.json(ctx, "POST", "/api/v1/drop", q, map[string]any{"files": files})
}

func editFile(ctx context.Context, s *Server, a map[string]any) (*toolResult, error) {
	site, _ := a["site"].(string)
	p, _ := a["path"].(string)
	find, _ := a["find"].(string)
	repl, hasRepl := a["replace"].(string)
	if site == "" || p == "" || !hasRepl {
		return errResult("site, path and replace are required"), nil
	}
	fp := "/api/v1/sites/" + url.PathEscape(site) + "/files/" + escapePath(p)
	content := repl
	if find != "" {
		resp, err := s.Backend.Do(ctx, &Request{Method: "GET", Path: fp})
		if err != nil {
			return nil, err
		}
		if resp.Status >= 400 {
			return ResultFrom(resp), nil
		}
		if !utf8.Valid(resp.Body) {
			return errResult(p + " is a binary file; use deploy_files to replace it"), nil
		}
		old := string(resp.Body)
		switch n := strings.Count(old, find); n {
		case 0:
			return errResult("the text to find isn't in " + p + "; read the file again and copy it exactly"), nil
		case 1:
			content = strings.Replace(old, find, repl, 1)
		default:
			return errResult(fmt.Sprintf("the text to find occurs %d times in %s; include more surrounding text so it is unique", n, p)), nil
		}
	}
	resp, err := s.Backend.Do(ctx, &Request{Method: "PUT", Path: fp, Body: []byte(content), ContentType: "application/octet-stream"})
	if err != nil {
		return nil, err
	}
	if resp.Status >= 400 {
		return ResultFrom(resp), nil
	}
	if pub, _ := a["publish"].(bool); pub {
		msg, _ := a["message"].(string)
		if msg == "" {
			msg = "Edited " + p
		}
		return s.json(ctx, "POST", "/api/v1/sites/"+url.PathEscape(site)+"/draft/publish", nil, map[string]any{"message": msg})
	}
	return textResult(fmt.Sprintf("Saved %s to the draft of %s. Preview: %s/s/%s/?draft=1 (publish with drafts_publish).", p, site, s.Backend.BaseURL(), site)), nil
}

func escapePath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	return strings.Join(segs, "/")
}

// ---- resources ----

type resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

var docResources = []resource{
	{URI: "bunker://docs/agent-guide", Name: "agent-guide", Title: "Bunker guide for agents", Description: "Authentication, deploying, sandbox rules, SDK and every API operation", MimeType: "text/markdown"},
	{URI: "bunker://docs/sdk", Name: "sdk", Title: "window.bunker SDK reference", MimeType: "text/markdown"},
	{URI: "bunker://docs/sandbox", Name: "sandbox", Title: "Sandbox rules for pages", MimeType: "text/markdown"},
}

func resourceTemplates() []map[string]any {
	return []map[string]any{
		{"uriTemplate": "bunker://sites/{site}", "name": "site", "title": "A site's details and file list", "mimeType": "application/json"},
		{"uriTemplate": "bunker://sites/{site}/files/{path}", "name": "site-file", "title": "One file of a site's working copy"},
	}
}

func (s *Server) listResources(ctx context.Context) (any, error) {
	out := append([]resource(nil), docResources...)
	resp, err := s.Backend.Do(ctx, &Request{Method: "GET", Path: "/api/v1/sites", Query: url.Values{"filter": {"all"}}})
	if err == nil && resp.Status == 200 {
		var l struct {
			Sites []struct {
				Slug, Title, Role string
			} `json:"sites"`
		}
		if json.Unmarshal(resp.Body, &l) == nil {
			for i, st := range l.Sites {
				if i >= 100 {
					break
				}
				if st.Role == "viewer" {
					continue // file access needs editor rights
				}
				out = append(out, resource{URI: "bunker://sites/" + st.Slug, Name: st.Slug, Title: st.Title, Description: "Site details and files", MimeType: "application/json"})
			}
		}
	}
	return map[string]any{"resources": out}, nil
}

func (s *Server) readResource(ctx context.Context, uri string) (any, error) {
	base := s.Backend.BaseURL()
	one := func(mt, text string) any {
		return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mt, "text": text}}}
	}
	switch uri {
	case "bunker://docs/agent-guide":
		ops, _ := s.Backend.Ops(ctx)
		return one("text/markdown", AgentGuide(base, ops, true)), nil
	case "bunker://docs/sdk":
		return one("text/markdown", SDKReference), nil
	case "bunker://docs/sandbox":
		return one("text/markdown", SandboxRules), nil
	}
	rest, ok := strings.CutPrefix(uri, "bunker://sites/")
	if !ok || rest == "" {
		return nil, &RPCError{Code: -32002, Message: "resource not found: " + uri}
	}
	site, file, hasFile := strings.Cut(rest, "/files/")
	site = strings.TrimSuffix(site, "/")
	if !hasFile {
		info, err := s.Backend.Do(ctx, &Request{Method: "GET", Path: "/api/v1/sites/" + url.PathEscape(site)})
		if err != nil {
			return nil, err
		}
		if info.Status >= 400 {
			_, msg := APIError(info)
			return nil, &RPCError{Code: -32002, Message: msg}
		}
		files, err := s.Backend.Do(ctx, &Request{Method: "GET", Path: "/api/v1/sites/" + url.PathEscape(site) + "/files"})
		if err != nil {
			return nil, err
		}
		var siteObj, filesObj any
		json.Unmarshal(info.Body, &siteObj)
		if files.Status == 200 {
			json.Unmarshal(files.Body, &filesObj)
		}
		b, _ := json.MarshalIndent(map[string]any{"site": siteObj, "files": filesObj}, "", "  ")
		return one("application/json", string(b)), nil
	}
	if f, err := url.PathUnescape(file); err == nil {
		file = f
	}
	resp, err := s.Backend.Do(ctx, &Request{Method: "GET", Path: "/api/v1/sites/" + url.PathEscape(site) + "/files/" + escapePath(file)})
	if err != nil {
		return nil, err
	}
	if resp.Status >= 400 {
		_, msg := APIError(resp)
		return nil, &RPCError{Code: -32002, Message: msg}
	}
	mt := guessMime(file)
	if utf8.Valid(resp.Body) {
		return one(mt, string(resp.Body)), nil
	}
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mt, "blob": base64.StdEncoding.EncodeToString(resp.Body)}}}, nil
}

func guessMime(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return "application/octet-stream"
	}
	switch strings.ToLower(p[i:]) {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js", ".mjs":
		return "text/javascript"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".txt", ".csv":
		return "text/plain"
	}
	return "application/octet-stream"
}

// ---- prompts ----

func prompts() []map[string]any {
	return []map[string]any{{
		"name":        "build_app",
		"title":       "Build a Bunker app",
		"description": "Plan, write and publish an internal web app on Bunker",
		"arguments": []map[string]any{
			{"name": "description", "description": "What the app should do, in plain words", "required": true},
			{"name": "site", "description": "Existing site to change (optional)", "required": false},
		},
	}}
}

func getPrompt(base, name string, args map[string]string) (any, error) {
	if name != "build_app" {
		return nil, &RPCError{Code: codeInvalidParams, Message: "unknown prompt: " + name}
	}
	desc := strings.TrimSpace(args["description"])
	if desc == "" {
		desc = "(ask me what the app should do)"
	}
	target := "Create a new site with create_site_from_html (one file) or deploy_files (several files)."
	if site := strings.TrimSpace(args["site"]); site != "" {
		target = "Change the existing site `" + site + "`: read its files (files_list, files_read), then use edit_file for small changes or deploy_files with site=" + site + " for a rewrite."
	}
	text := "Build an internal web app on Bunker (" + base + ").\n\nWhat it should do: " + desc + "\n\n" + target + `

Steps:
1. Plan the screens and the data (bunker.db collections, their rules, any file uploads or AI calls).
2. Write a polished, accessible, mobile-friendly page. Keep it self-contained: inline CSS and JS, no CDN scripts, no fetch() to outside URLs.
3. Store shared data with bunker.db (live updates with subscribe), per-person settings with bunker.storage, and greet the viewer with bunker.me().
4. Publish, then give me the URL. Offer to share it (sharing_add) with people or groups.

` + SandboxRules + "\n\n" + SDKReference
	return map[string]any{
		"description": "Build a Bunker app",
		"messages":    []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": text}}},
	}, nil
}

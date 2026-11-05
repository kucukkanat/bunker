package sites

import (
	"archive/zip"
	"net/http"
	"strconv"
	"strings"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() { extraOps = registerExtra }

var extraOps func(a *core.App)

func registerExtra(a *core.App) {
	a.Handle(core.Op{Name: "sites.duplicate", Method: "POST", Path: "/api/v1/sites/{site}/duplicate", Summary: "Make a copy of a site (files only, not its data)",
		Params: []core.Param{siteParam, {Name: "title", In: "body", Desc: "Name of the copy"}},
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			s, _, err := Full(r.Context(), u, r.PathValue("site"), core.RoleEditor)
			if err != nil {
				return nil, err
			}
			var in struct{ Title string }
			_ = core.Decode(r, &in)
			if in.Title == "" {
				in.Title = s.Title + " (copy)"
			}
			m, _, err := WorkingManifest(r.Context(), s)
			if err != nil {
				return nil, err
			}
			ns, err := CreateSite(r.Context(), u, in.Title, "", s.Description, s.Icon)
			if err != nil {
				return nil, err
			}
			if len(m) > 0 {
				if _, err := Commit(r.Context(), u, ns, m, "Copied from "+s.Title, "duplicate", true); err != nil {
					return nil, err
				}
				SetKind(r.Context(), ns.ID, s.Kind)
			}
			ns, _ = GetByID(r.Context(), ns.ID, false)
			return core.Created{V: ns}, nil
		}})

	a.Handle(core.Op{Name: "sites.export", Method: "GET", Path: "/api/v1/sites/{site}/export.zip", Summary: "Download a site's files as a zip",
		Params: []core.Param{siteParam, {Name: "version", In: "query", Type: "integer", Desc: "Version (default: draft if any, else live)"}},
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
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+s.Slug+`.zip"`)
			w.Header().Set("Cache-Control", "no-store")
			zw := zip.NewWriter(w)
			for _, p := range m.Paths() {
				f := m[p]
				fh, err := app.Blobs.Open(f.Hash)
				if err != nil {
					continue
				}
				method := zip.Deflate
				if !compressible(f.Type) {
					method = zip.Store
				}
				zf, err := zw.CreateHeader(&zip.FileHeader{Name: p, Method: method})
				if err == nil {
					copyN(zf, fh)
				}
				fh.Close()
			}
			zw.Close()
			app.Audit(r.Context(), "site.export", s.ID, s.Slug, nil)
		}})
}

func copyN(w interface{ Write([]byte) (int, error) }, r interface{ Read([]byte) (int, error) }) {
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

var _ = strings.TrimSpace

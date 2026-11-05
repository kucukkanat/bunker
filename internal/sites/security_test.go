package sites_test

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/kucukkanat/bunker/internal/testkit"
)

// Each zip in a multipart upload was limited on its own, so many small,
// highly compressed zips in one request expanded far past the limit in
// memory before anything checked the total.
func TestManyZipsShareOneLimit(t *testing.T) {
	t.Setenv("BUNKER_SITE_MAX_MB", "1")
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i < 3; i++ {
		var zb bytes.Buffer
		zw := zip.NewWriter(&zb)
		w, _ := zw.Create("d" + string(rune('a'+i)) + "/big.txt")
		w.Write(bytes.Repeat([]byte("A"), 600<<10))
		zw.Close()
		fw, _ := mw.CreateFormFile("files", "part"+string(rune('a'+i))+".zip")
		fw.Write(zb.Bytes())
	}
	mw.Close()
	r := ada.Do("POST", "/api/v1/drop", body.Bytes(), "Content-Type", mw.FormDataContentType())
	if r.Status != 413 || !strings.Contains(string(r.Body), "expands") {
		t.Fatalf("3 zips x 600 KB under a 1 MB limit: %d %s", r.Status, r.Body)
	}
}

// Offering a site as a template publishes its files to everyone, like
// organization-wide viewing, which only the owner may turn on.
func TestOnlyOwnerOffersTemplate(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	bob := k.As("bob@corp.com", "")
	carol := k.As("carol@corp.com", "")
	res := ada.Must("POST", "/api/v1/drop", map[string]any{"title": "Payroll", "files": map[string]string{"index.html": "<h1>salaries</h1>"}})
	slug := res["slug"].(string)
	ada.Must("POST", "/api/v1/sites/"+slug+"/members", map[string]any{"type": "user", "id": "bob@corp.com", "role": "editor"})
	if r := bob.Do("PATCH", "/api/v1/sites/"+slug, map[string]any{"isTemplate": true}); r.Status != 403 {
		t.Fatalf("editor offered the site as a template: %d %s", r.Status, r.Body)
	}
	if r := carol.Do("POST", "/api/v1/sites", map[string]any{"title": "copy", "template": slug}); r.Status < 400 {
		t.Fatalf("outsider copied a private site: %d %s", r.Status, r.Body)
	}
	ada.Must("PATCH", "/api/v1/sites/"+slug, map[string]any{"isTemplate": true})
	carol.Must("POST", "/api/v1/sites", map[string]any{"title": "copy", "template": slug})
}

// Trashing a site renames it to trash-<id>-<slug>. If anyone could take
// that address first, the owner's delete silently did nothing.
func TestTrashAddressesAreReserved(t *testing.T) {
	k := testkit.New(t)
	ada := k.As("ada@corp.com", "")
	mal := k.As("mal@corp.com", "")
	res := ada.Must("POST", "/api/v1/sites", map[string]any{"title": "Payroll", "slug": "payroll"})
	id := int(res["id"].(float64))
	if r := mal.Do("POST", "/api/v1/sites", map[string]any{"title": "x", "slug": "trash-" + itoa(id) + "-payroll"}); r.Status < 400 {
		t.Fatalf("took a trash address: %d", r.Status)
	}
	if r := mal.Do("POST", "/api/v1/sites", map[string]any{"title": "trash " + itoa(id) + " payroll"}); r.Status != 201 || strings.HasPrefix(r.JSON()["slug"].(string), "trash-") {
		t.Fatalf("title-derived trash address: %d %s", r.Status, r.Body)
	}
	ada.Must("DELETE", "/api/v1/sites/payroll", nil)
	if r := ada.Do("GET", "/api/v1/sites/payroll", nil); r.Status != 404 {
		t.Fatalf("site still there after delete: %d", r.Status)
	}
}


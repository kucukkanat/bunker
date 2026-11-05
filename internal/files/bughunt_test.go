package files_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"testing"
)

// Several files uploaded in one request were stored with the same
// millisecond timestamp, and the list cursor (created_at < before) then
// skipped the ones that fell on a page boundary.
func TestUploadListPagesDontSkipFiles(t *testing.T) {
	slug, _, ann, _ := setup(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i := 0; i < 10; i++ {
		w, _ := mw.CreateFormFile("file", fmt.Sprintf("note-%d.txt", i))
		fmt.Fprintf(w, "note %d", i)
	}
	mw.Close()
	if c, b, _ := call(t, ann, "POST", "/api/v1/sites/"+slug+"/uploads", &buf, mw.FormDataContentType()); c != 201 {
		t.Fatalf("upload: %d %s", c, b)
	}
	seen := map[string]bool{}
	before := ""
	for page := 0; page < 20; page++ {
		path := "/api/v1/sites/" + slug + "/uploads?limit=3"
		if before != "" {
			path += "&before=" + before
		}
		c, b, _ := call(t, ann, "GET", path, nil, "")
		if c != 200 {
			t.Fatalf("list: %d %s", c, b)
		}
		var res struct {
			Items []file
			Next  *int64
		}
		json.Unmarshal(b, &res)
		for _, f := range res.Items {
			seen[f.Name] = true
		}
		if res.Next == nil {
			break
		}
		before = fmt.Sprint(*res.Next)
	}
	if len(seen) != 10 {
		t.Errorf("paging saw %d of 10 files: %v", len(seen), seen)
	}
}

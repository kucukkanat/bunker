package apptypes

import (
	"strings"
	"testing"
	"time"
)

// Goldmark is quadratic on some shapes; before the budget, 200 KB of
// "[a](" took ~17 s and 50,000 nested list markers ~12 s per file, from
// any signed-in person's deploy.
func TestMarkdownHostileInputsStayFast(t *testing.T) {
	inputs := map[string]string{
		"links":  strings.Repeat("[a](", 50000),
		"lists":  strings.Repeat("- ", 50000) + "x",
		"quotes": strings.Repeat(">", 100000) + " x",
		"refs":   strings.Repeat("[a]: /b\n", 50000) + strings.Repeat("[a]", 50000),
		"olist":  strings.Repeat("1. ", 40000) + "x",
	}
	for name, src := range inputs {
		files := map[string][]byte{"index.md": []byte(src)}
		st := time.Now()
		if _, err := renderMarkdown(files, []string{"index.md"}); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(st); d > time.Second {
			t.Errorf("%s took %v", name, d)
		}
	}
	// Ordinary documents still render as markdown.
	files := map[string][]byte{"index.md": []byte("# Hi\n\n- a\n  - b\n\n> quote\n\n[link](https://example.com)\n")}
	renderMarkdown(files, []string{"index.md"})
	if h := string(files["index.html"]); !strings.Contains(h, "<blockquote>") || !strings.Contains(h, `href="https://example.com"`) {
		t.Fatalf("normal markdown not rendered: %s", h)
	}
}

func TestNotebookMarkdownCellsBudgeted(t *testing.T) {
	cell := `{"cell_type":"markdown","metadata":{},"source":["` + strings.Repeat("[a](", 20000) + `"]}`
	nb := `{"nbformat":4,"metadata":{},"cells":[` + strings.Repeat(cell+",", 9) + cell + `]}`
	st := time.Now()
	if _, err := notebookPage("x.ipynb", []byte(nb), 0, newMDBudget()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(st); d > 2*time.Second {
		t.Fatalf("notebook took %v", d)
	}
}

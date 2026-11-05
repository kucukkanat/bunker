package sites

import "testing"

func TestGuessTitle(t *testing.T) {
	cases := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"my-site/about.html": "<title>About</title>", "my-site/index.html": "<title>Folder site</title>"}, "Folder site"},
		{map[string]string{"team-notes/a.css": "", "team-notes/b.js": ""}, "Team notes"},
		{map[string]string{"meeting-notes.md": "intro\n# Team meeting notes\n\ntext"}, "Team meeting notes"},
		{map[string]string{"handbook/README.md": "---\ntitle: Team Handbook\n---\n# Welcome\n", "handbook/guide/a.md": "# A"}, "Team Handbook"},
		{map[string]string{"sales_q3.csv": "a,b"}, "Sales q3"},
		{map[string]string{"a.png": "", "b.jpg": ""}, "Gallery"},
		{map[string]string{"index.html": "<p>hi</p>"}, "Untitled site"},
	}
	for _, c := range cases {
		files := map[string][]byte{}
		for k, v := range c.files {
			files[k] = []byte(v)
		}
		if got := guessTitle(files); got != c.want {
			t.Errorf("guessTitle(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}

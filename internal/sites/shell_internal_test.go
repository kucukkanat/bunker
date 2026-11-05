package sites

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// renderShell must be byte-identical to executing shellTmpl.
func TestShellMatchesTemplate(t *testing.T) {
	shellOnce.Do(compileShell)
	if shellParts == nil {
		t.Fatal("shell template didn't compile into parts")
	}
	samples := []string{
		"", "Plain title", `Quotes " and ' and <b>tags</b> & amps + plus`, "naïve café 🍕 日本語",
		"\x00nul", "bad utf8 \xff\xfe end", "javascript:alert(1)", "JavaScript:x", "mailto:a@b", "HTTPS://x.y/z",
		"/s/site/~r/1-2-3-4-AbC_d-ef/page with space.html?a=1&b=2+3#frag", "100%", "%zz %4", "%41%7e", "a:b/c", "/a:b",
		"\ufffd\ufdd0\uffff", "{{.Title}}", "zqx1qxz", "tab\tnewline\n", "back\\slash", "[]@!$*(),;=",
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 300; i++ {
		b := make([]byte, rng.IntN(24))
		for k := range b {
			b[k] = byte(rng.IntN(256))
		}
		samples = append(samples, string(b))
	}
	pick := func() string { return samples[rng.IntN(len(samples))] }
	for i := 0; i < 3000; i++ {
		d := shellData{Title: pick(), Slug: pick(), Src: pick(), Base: pick(), Role: pick(), Icon: pick(), User: pick(),
			Draft: rng.IntN(2) == 0, Badge: rng.IntN(2) == 0, Version: rng.Int64N(1000) - 5, Live: rng.Int64(), SiteID: rng.Int64N(1 << 40)}
		if i < len(samples) {
			d.Title, d.Src, d.Icon = samples[i], samples[i], samples[i]
		}
		var want, got strings.Builder
		if err := shellTmpl.Execute(&want, d.values()); err != nil {
			t.Fatal(err)
		}
		renderShell(&got, d)
		if got.String() != want.String() {
			t.Fatalf("mismatch for %+v:\n got %q\nwant %q", d, got.String(), want.String())
		}
	}
}

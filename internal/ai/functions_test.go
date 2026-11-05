package ai

import (
	"strings"
	"testing"
)

func TestFunctionFileNotes(t *testing.T) {
	for p, want := range map[string]bool{"api/orders.js": true, "api/orders/refund.ts": true, "api/_shared.mjs": true, "api/data.json": false, "app.js": false, "js/api/x.js": false} {
		if isFunctionFile(p) != want {
			t.Errorf("isFunctionFile(%q) = %v", p, !want)
		}
	}
	ok := functionNote("api/orders/refund.ts", []byte("export default async function (input, ctx) { return 1 }"))
	if !strings.Contains(ok, `bunker.fn("orders/refund"`) || strings.Contains(ok, "WARNING") {
		t.Fatalf("good function: %s", ok)
	}
	bad := functionNote("api/x.js", []byte("import _ from 'lodash';\nfunction go() { document.title = 1 }"))
	for _, w := range []string{"export default", "document", "npm"} {
		if !strings.Contains(bad, w) {
			t.Fatalf("missing %q warning: %s", w, bad)
		}
	}
	if n := functionNote("api/_util.js", []byte("export const a = 1")); strings.Contains(n, "WARNING") || !strings.Contains(n, "shared module") {
		t.Fatalf("shared module: %s", n)
	}
	if n := functionNote("api/y.js", []byte("import { a } from './_util.js'\nexport default () => a")); strings.Contains(n, "WARNING") {
		t.Fatalf("relative import flagged: %s", n)
	}
}

func TestBuilderPromptTeachesFunctions(t *testing.T) {
	for _, w := range []string{"bunker.fn(", "export const config", "bunker.secrets.get", "config.elevated", "never write a key"} {
		if !strings.Contains(builderSystemPrompt, w) {
			t.Fatalf("builder prompt misses %q", w)
		}
	}
}

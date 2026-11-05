package ai

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuilderPreviewErrors(t *testing.T) {
	resetUsage()
	f := newFake(t)
	configure(t, map[string]any{"provider": "anthropic", "apiKey": "sk-ant-test-key-123456", "endpoint": f.URL})
	slug := newSite(t, "Error Fix")
	f.push(anthropicTurn("Fixed the typo in app.js."))
	errs := []any{}
	for i := 0; i < 25; i++ {
		errs = append(errs, map[string]any{"message": fmt.Sprintf("ReferenceError: totl%d is not defined", i), "source": "app.js", "line": 12})
	}
	rec := call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "Fix these errors", "errors": errs})
	if !strings.Contains(rec.Body.String(), `"type":"done"`) {
		t.Fatalf("builder: %s", rec.Body.String())
	}
	msg := fmt.Sprint(f.req(0)["messages"])
	if !strings.Contains(msg, "<preview_errors>") || !strings.Contains(msg, "totl24 is not defined (at app.js:12)") {
		t.Fatalf("errors not passed to the model: %s", msg)
	}
	if strings.Contains(msg, "totl4 is not defined") {
		t.Fatal("only the last 20 errors should be sent")
	}
	// Without errors there is no block in the new turn.
	f.push(anthropicTurn("ok"))
	call(t, "POST", "/api/v1/sites/"+slug+"/builder", map[string]any{"message": "hello"})
	ms := f.req(1)["messages"].([]any)
	if strings.Contains(fmt.Sprint(ms[len(ms)-1]), "<preview_errors>") {
		t.Fatal("unexpected preview_errors block")
	}
}

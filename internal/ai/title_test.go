package ai

import "testing"

func TestFallbackTitle(t *testing.T) {
	for in, want := range map[string]string{
		"A page where my team can book the meeting room":                         "Book the meeting room",
		"A form where people request time off, and a list for managers":          "Request time off",
		"Expense tracker with monthly totals":                                    "Expense tracker with monthly totals",
		"Please make me a simple app to log expenses with category and receipts": "Log expenses with category",
		"a": "New app",
		"":  "New app",
	} {
		if got := fallbackTitle(in); got != want {
			t.Errorf("fallbackTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

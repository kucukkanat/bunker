package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// isTTY reports whether f is an interactive terminal.
func isTTY(f any) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	st, err := file.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

type styler struct{ on bool }

func (s styler) wrap(code, t string) string {
	if !s.on || t == "" {
		return t
	}
	return "\x1b[" + code + "m" + t + "\x1b[0m"
}
func (s styler) bold(t string) string   { return s.wrap("1", t) }
func (s styler) dim(t string) string    { return s.wrap("2", t) }
func (s styler) red(t string) string    { return s.wrap("31", t) }
func (s styler) green(t string) string  { return s.wrap("32", t) }
func (s styler) yellow(t string) string { return s.wrap("33", t) }
func (s styler) cyan(t string) string   { return s.wrap("36", t) }
func (s styler) moss(t string) string   { return s.wrap("38;5;65", t) }
func (s styler) url(t string) string    { return s.wrap("4;36", t) }

const (
	symOK   = "✓"
	symErr  = "✗"
	symWarn = "!"
	symInfo = "→"
)

// Printing helpers. Human output goes to stderr for status lines and to
// stdout for results, so `--json` and pipes stay clean.

func (x *App) ok(format string, a ...any) {
	if x.JSON {
		return
	}
	fmt.Fprintln(x.Stderr, x.errSt.green(symOK)+" "+fmt.Sprintf(format, a...))
}

func (x *App) info(format string, a ...any) {
	if x.JSON {
		return
	}
	fmt.Fprintln(x.Stderr, x.errSt.dim(symInfo)+" "+fmt.Sprintf(format, a...))
}

func (x *App) warn(format string, a ...any) {
	if x.JSON {
		return
	}
	fmt.Fprintln(x.Stderr, x.errSt.yellow(symWarn)+" "+fmt.Sprintf(format, a...))
}

// out prints a result line to stdout (suppressed in --json mode).
func (x *App) out(format string, a ...any) {
	if x.JSON {
		return
	}
	fmt.Fprintf(x.Stdout, format+"\n", a...)
}

func (x *App) printJSON(v any) error {
	enc := json.NewEncoder(x.Stdout)
	enc.SetEscapeHTML(false)
	if isTTY(x.Stdout) {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

// result prints v as JSON in --json mode, otherwise calls human.
func (x *App) result(v any, human func()) error {
	if x.JSON {
		return x.printJSON(v)
	}
	human()
	return nil
}

// ---- spinner ----

type spinner struct {
	x      *App
	mu     sync.Mutex
	msg    string
	active bool
	stop   chan struct{}
	done   chan struct{}
}

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spin starts a spinner on stderr (a no-op unless stderr is a terminal).
func (x *App) spin(msg string) *spinner {
	s := &spinner{x: x, msg: msg}
	if x.JSON || !x.errTTY {
		return s
	}
	s.active = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			s.mu.Lock()
			fmt.Fprintf(x.Stderr, "\r\x1b[2K%s %s", x.errSt.moss(frames[i%len(frames)]), s.msg)
			s.mu.Unlock()
			i++
			select {
			case <-s.stop:
				fmt.Fprint(x.Stderr, "\r\x1b[2K")
				return
			case <-t.C:
			}
		}
	}()
	return s
}

func (s *spinner) update(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// stopSpin clears the spinner line.
func (s *spinner) end() {
	if !s.active {
		return
	}
	s.active = false
	close(s.stop)
	<-s.done
}

func (s *spinner) succeed(format string, a ...any) {
	s.end()
	s.x.ok(format, a...)
}

// ---- prompts ----

// confirm asks a yes/no question. --yes answers yes; non-interactive
// sessions must pass --yes.
func (x *App) confirm(q string) error {
	if x.Yes {
		return nil
	}
	if !isTTY(x.Stdin) {
		return usageErr("this needs confirmation; add --yes to run it without a prompt")
	}
	fmt.Fprintf(x.Stderr, "%s %s %s ", x.errSt.yellow("?"), q, x.errSt.dim("[y/N]"))
	line, _ := bufio.NewReader(x.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	if a == "y" || a == "yes" {
		return nil
	}
	return errCancelled
}

func (x *App) prompt(q string) string {
	fmt.Fprintf(x.Stderr, "%s %s ", x.errSt.cyan("?"), q)
	line, _ := bufio.NewReader(x.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}

// ---- tables & formatting ----

func (x *App) table(header []string, rows [][]string) {
	if x.JSON {
		return
	}
	w := make([]int, len(header))
	for i, h := range header {
		w[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(w) && utf8.RuneCountInString(stripANSI(c)) > w[i] {
				w[i] = utf8.RuneCountInString(stripANSI(c))
			}
		}
	}
	line := func(cells []string, style func(string) string) {
		var b strings.Builder
		for i, c := range cells {
			pad := w[i] - utf8.RuneCountInString(stripANSI(c))
			if i == len(cells)-1 {
				pad = 0
			}
			b.WriteString(style(c) + strings.Repeat(" ", pad))
			if i < len(cells)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(x.Stdout, strings.TrimRight(b.String(), " "))
	}
	line(header, x.outSt.dim)
	for _, r := range rows {
		line(r, func(s string) string { return s })
	}
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

func relTime(unix int64) string {
	if unix == 0 {
		return "never"
	}
	d := time.Since(time.Unix(unix, 0))
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		s = plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		s = plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		s = plural(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		s = plural(int(d.Hours()/24/30), "month")
	default:
		s = plural(int(d.Hours()/24/365), "year")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return fmt.Sprintf("%d %ss", n, w)
}

func clipStr(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

var _ io.Writer = (*os.File)(nil)

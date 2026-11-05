package cli

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Args are a command's parsed positional arguments and flags.
type Args struct {
	Pos   []string
	flags map[string][]string
	order []string
}

func (a *Args) set(name, v string) {
	if a.flags == nil {
		a.flags = map[string][]string{}
	}
	if _, ok := a.flags[name]; !ok {
		a.order = append(a.order, name)
	}
	a.flags[name] = append(a.flags[name], v)
}

// Has reports whether the flag was given.
func (a *Args) Has(n string) bool { _, ok := a.flags[n]; return ok }

// Str returns the flag's last value ("" if absent).
func (a *Args) Str(n string) string {
	v := a.flags[n]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// All returns every value of a repeated flag.
func (a *Args) All(n string) []string { return a.flags[n] }

// Bool interprets a flag as a boolean.
func (a *Args) Bool(n string) bool {
	if !a.Has(n) {
		return false
	}
	switch strings.ToLower(a.Str(n)) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// Int parses a flag as an integer.
func (a *Args) Int(n string, def int) (int, error) {
	if !a.Has(n) {
		return def, nil
	}
	v, err := strconv.Atoi(a.Str(n))
	if err != nil {
		return 0, usageErr("--%s needs a number", n)
	}
	return v, nil
}

// Names lists given flags in order.
func (a *Args) Names() []string { return a.order }

// Arg returns positional i or "".
func (a *Args) Arg(i int) string {
	if i < len(a.Pos) {
		return a.Pos[i]
	}
	return ""
}

func lookupFlag(c *command, name string, short bool) *flagDoc {
	for i := range c.Flags {
		f := &c.Flags[i]
		if (!short && f.Name == name) || (short && f.Short != "" && f.Short == name) {
			return f
		}
	}
	return nil
}

// parseArgs parses a command's arguments. Flags may appear anywhere;
// "--" ends flag parsing; "-" is a positional (stdin).
func parseArgs(args []string, c *command) (*Args, error) {
	a := &Args{}
	for i := 0; i < len(args); i++ {
		t := args[i]
		if t == "--" {
			a.Pos = append(a.Pos, args[i+1:]...)
			break
		}
		if t == "-" || !strings.HasPrefix(t, "-") || isNumber(t) {
			a.Pos = append(a.Pos, t)
			continue
		}
		short := !strings.HasPrefix(t, "--")
		name := strings.TrimLeft(t, "-")
		val, hasVal := "", false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasVal = k, v, true
		}
		if name == "help" || (short && name == "h") {
			a.set("help", "true")
			continue
		}
		f := lookupFlag(c, name, short)
		if f == nil && !short && strings.HasPrefix(name, "no-") {
			if nf := lookupFlag(c, strings.TrimPrefix(name, "no-"), false); nf != nil && nf.Arg == "" {
				a.set(nf.Name, "false")
				continue
			}
		}
		if f == nil {
			if !c.Loose || short {
				msg := "unknown flag " + t
				var names []string
				for _, fl := range c.Flags {
					names = append(names, fl.Name)
				}
				if s := suggest(name, names); s != "" {
					msg += " (did you mean --" + s + "?)"
				}
				return nil, usageErr("%s", msg)
			}
			// Loose: --key value | --key=value | --key (true)
			if !hasVal {
				if i+1 < len(args) && (!strings.HasPrefix(args[i+1], "-") || args[i+1] == "-" || isNumber(args[i+1])) {
					val = args[i+1]
					i++
				} else {
					val = "true"
				}
			}
			a.set(name, val)
			continue
		}
		switch {
		case f.Arg == "":
			if !hasVal {
				val = "true"
			}
		case strings.HasPrefix(f.Arg, "?"):
			if !hasVal && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				val = args[i+1]
				i++
			}
		default:
			if !hasVal {
				if i+1 >= len(args) {
					return nil, usageErr("--%s needs a value (<%s>)", f.Name, f.Arg)
				}
				val = args[i+1]
				i++
			}
		}
		a.set(f.Name, val)
	}
	return a, nil
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// parseGlobals strips global flags from anywhere before "--".
func (x *App) parseGlobals(args []string) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		t := args[i]
		if t == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(t, "-"), "=")
		if !strings.HasPrefix(t, "-") || t == "-" {
			rest = append(rest, t)
			continue
		}
		long := strings.HasPrefix(t, "--")
		switch {
		case long && name == "json":
			x.JSON = !hasVal || val != "false"
		case (long && name == "yes") || (!long && name == "y"):
			x.Yes = true
		case long && name == "debug":
			x.Debug = true
		case long && name == "no-color":
			x.noColor = true
		case (long && name == "help") || (!long && name == "h"):
			if len(rest) == 0 {
				x.help = true
			} else {
				rest = append(rest, "--help")
			}
		case long && (name == "server" || name == "profile"):
			if !hasVal {
				if i+1 >= len(args) {
					return nil, usageErr("--%s needs a value", name)
				}
				val = args[i+1]
				i++
			}
			if name == "server" {
				x.Server = val
			} else {
				x.Profile = val
			}
		default:
			rest = append(rest, t)
		}
	}
	return rest, nil
}

func jsonEncoder(w io.Writer) *json.Encoder {
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	return e
}

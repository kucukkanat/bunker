package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openBrowser opens url in the default browser; returns false if it couldn't.
func openBrowser(url string) bool {
	if os.Getenv("BUNKER_NO_BROWSER") != "" {
		return false
	}
	var cmd *exec.Cmd
	if b := os.Getenv("BROWSER"); b != "" {
		cmd = exec.Command(b, url)
	} else {
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
				return false
			}
			cmd = exec.Command("xdg-open", url)
		}
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	go cmd.Wait()
	return true
}

// copyToClipboard copies text using the platform's clipboard tool.
func copyToClipboard(text string) bool {
	if os.Getenv("BUNKER_NO_CLIPBOARD") != "" {
		return false
	}
	var cands [][]string
	switch runtime.GOOS {
	case "darwin":
		cands = [][]string{{"pbcopy"}}
	case "windows":
		cands = [][]string{{"clip"}}
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			cands = append(cands, []string{"wl-copy"})
		}
		if os.Getenv("DISPLAY") != "" {
			cands = append(cands, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
		}
		if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
			cands = append(cands, []string{"clip.exe"})
		}
	}
	for _, c := range cands {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

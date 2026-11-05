package mail_test

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/kucukkanat/bunker/internal/mail"
	_ "github.com/kucukkanat/bunker/internal/notify"
	"github.com/kucukkanat/bunker/internal/testkit"
)

// fakeSMTP accepts one plain-text SMTP session per connection and records DATA.
func fakeSMTP(t *testing.T) (string, func() []string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := func(s string) { c.Write([]byte(s + "\r\n")) }
				w("220 fake")
				var data strings.Builder
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" {
							inData = false
							mu.Lock()
							got = append(got, data.String())
							mu.Unlock()
							w("250 ok")
							continue
						}
						data.WriteString(line)
						continue
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						w("250 fake")
					case cmd == "DATA":
						inData = true
						w("354 go")
					case cmd == "QUIT":
						w("221 bye")
						return
					default:
						w("250 ok")
					}
				}
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

func TestInviteAndNotificationEmails(t *testing.T) {
	addr, mails := fakeSMTP(t)
	t.Setenv("BUNKER_SMTP_URL", "smtp://"+addr)
	t.Setenv("BUNKER_SMTP_FROM", "Bunker <bunker@corp.com>")
	k := testkit.New(t)
	admin := k.As("", "")
	st := admin.Must("GET", "/api/v1/me/email-notifications", nil)
	if st["available"] != true {
		t.Fatalf("mail not available: %v", st)
	}
	res := admin.Must("POST", "/api/v1/admin/invites", map[string]any{"email": "new@corp.com"})
	if res["emailed"] != true {
		t.Fatalf("invite not emailed: %v", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(mails()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	m := mails()
	if len(m) == 0 || !strings.Contains(m[0], "To: new@corp.com") || !strings.Contains(m[0], "/auth/invite?token=") {
		t.Fatalf("invite email: %v", m)
	}
	// Opt out → no email for notifications.
	ada := k.As("ada@corp.com", "")
	ada.Must("PUT", "/api/v1/me/email-notifications", map[string]any{"enabled": false})
	if g := ada.Must("GET", "/api/v1/me/email-notifications", nil); g["enabled"] != false {
		t.Errorf("pref not saved: %v", g)
	}
}

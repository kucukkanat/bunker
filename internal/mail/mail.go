// Package mail sends email through an SMTP server when one is configured
// (BUNKER_SMTP_URL or Admin → Email). It's optional: without it, invites
// and reset links are copied by hand and notifications stay in the app.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kucukkanat/bunker/internal/core"
)

func init() {
	core.Register(core.Module{
		Name:  "mail",
		Order: 15,
		Migrations: []string{`
CREATE TABLE mail_prefs (
  user_email TEXT PRIMARY KEY,
  off INTEGER NOT NULL DEFAULT 0
);
`},
		Init: initMail,
	})
}

var app *core.App

// Message is one email.
type Message struct {
	To      string
	Subject string
	Text    string
	// Button adds a call-to-action link to the HTML version.
	ButtonText, ButtonURL string
}

type config struct {
	URL  *url.URL
	From string
}

func cfg() (*config, error) {
	raw := core.Env("SMTP_URL", "")
	if raw == "" {
		raw = app.SecretSetting("mail.smtp_url")
	}
	if raw == "" {
		return nil, errors.New("not configured")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Hostname() == "" {
		return nil, errors.New("SMTP URL must look like smtp://user:password@smtp.example.com:587")
	}
	from := core.Env("SMTP_FROM", app.Setting("mail.from"))
	if from == "" && u.User != nil && strings.Contains(u.User.Username(), "@") {
		from = "Bunker <" + u.User.Username() + ">"
	}
	if from == "" {
		return nil, errors.New("set a From address (BUNKER_MAIL_FROM)")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, errors.New("the From address isn't valid")
	}
	return &config{URL: u, From: from}, nil
}

// Enabled reports whether email can be sent.
func Enabled() bool {
	if app == nil {
		return false
	}
	_, err := cfg()
	return err == nil
}

// WantsEmail reports whether a person hasn't turned email notifications off.
func WantsEmail(ctx context.Context, email string) bool {
	var off int
	app.DB.QueryRowContext(ctx, `SELECT off FROM mail_prefs WHERE user_email=?`, strings.ToLower(email)).Scan(&off)
	return off == 0
}

var queue = make(chan Message, 1000)

// Send queues an email; it returns false if email isn't configured or the
// queue is full. Delivery happens in the background with retries.
func Send(m Message) bool {
	if !Enabled() || m.To == "" {
		return false
	}
	select {
	case queue <- m:
		return true
	default:
		app.Log.Warn("mail queue full, dropping", "to", m.To)
		return false
	}
}

func initMail(a *core.App) error {
	app = a
	var once sync.Once
	stop := make(chan struct{})
	a.On("shutdown", func(context.Context, any) { once.Do(func() { close(stop) }) })
	go worker(stop)
	registerOps(a)
	return nil
}

func worker(stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case m := <-queue:
			var err error
			for i := 0; i < 3; i++ {
				if err = deliver(m); err == nil {
					break
				}
				time.Sleep(time.Duration(2<<i) * time.Second)
			}
			if err != nil {
				app.Log.Warn("email not delivered", "to", m.To, "err", err)
			}
		}
	}
}

func deliver(m Message) error {
	c, err := cfg()
	if err != nil {
		return err
	}
	msg, err := build(c.From, m)
	if err != nil {
		return err
	}
	from, _ := mail.ParseAddress(c.From)
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return err
	}
	host := c.URL.Hostname()
	port := c.URL.Port()
	if port == "" {
		port = map[string]string{"smtp": "587", "smtps": "465"}[c.URL.Scheme]
	}
	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if c.URL.User != nil {
		pw, _ := c.URL.User.Password()
		auth = smtp.PlainAuth("", c.URL.User.Username(), pw, host)
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if c.URL.Scheme == "smtps" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if c.URL.Scheme == "smtp" {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		} else if auth != nil && c.URL.Query().Get("insecure") != "1" {
			return errors.New("the SMTP server doesn't offer STARTTLS; refusing to send the password in clear text (add ?insecure=1 to allow)")
		}
	}
	if auth != nil {
		if err := cl.Auth(auth); err != nil {
			return err
		}
	}
	if err := cl.Mail(from.Address); err != nil {
		return err
	}
	if err := cl.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func build(from string, m Message) ([]byte, error) {
	if strings.ContainsAny(m.To+m.Subject, "\r\n") {
		return nil, errors.New("invalid header")
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nMessage-ID: <%s@bunker>\r\nAuto-Submitted: auto-generated\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n",
		from, m.To, mime.QEncoding.Encode("utf-8", m.Subject), time.Now().Format(time.RFC1123Z), core.RandomToken(12), mw.Boundary())
	text := m.Text
	if m.ButtonURL != "" {
		text += "\n\n" + m.ButtonText + ": " + m.ButtonURL
	}
	text += "\n\n-- \nSent by Bunker. You can turn off email notifications in Bunker → Settings.\n"
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Transfer-Encoding", "8bit")
	pw, _ := mw.CreatePart(h)
	pw.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n")))
	h = textproto.MIMEHeader{}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Transfer-Encoding", "8bit")
	hw, _ := mw.CreatePart(h)
	btn := ""
	if m.ButtonURL != "" && (strings.HasPrefix(m.ButtonURL, "https://") || strings.HasPrefix(m.ButtonURL, "http://")) {
		btn = fmt.Sprintf(`<p style="margin:24px 0"><a href="%s" style="background:#1f3a2e;color:#f7f4ec;padding:12px 20px;border-radius:10px;text-decoration:none;font-weight:600;display:inline-block">%s</a></p>`, html.EscapeString(m.ButtonURL), html.EscapeString(m.ButtonText))
	}
	paras := ""
	for _, p := range strings.Split(m.Text, "\n\n") {
		paras += "<p>" + strings.ReplaceAll(html.EscapeString(p), "\n", "<br>") + "</p>"
	}
	fmt.Fprintf(hw, `<!doctype html><html><body style="margin:0;background:#f7f4ec;padding:24px;font:16px/1.5 -apple-system,'Segoe UI',Inter,sans-serif;color:#1c211e">
<div style="max-width:520px;margin:0 auto;background:#fffdf8;border:1px solid #ddd6c4;border-radius:16px;padding:28px">
<div style="font:600 20px Georgia,serif;color:#1f3a2e;margin-bottom:12px">bunker</div>
<h1 style="font:600 22px/1.3 Georgia,serif;color:#1f3a2e;margin:0 0 12px">%s</h1>%s%s
<p style="color:#5a635d;font-size:13px;margin-top:28px">Sent by Bunker. You can turn off email notifications in Bunker → Settings.</p></div></body></html>`,
		html.EscapeString(m.Subject), paras, btn)
	mw.Close()
	return b.Bytes(), nil
}

func registerOps(a *core.App) {
	a.Handle(core.Op{Name: "admin.mail.get", Method: "GET", Path: "/api/v1/admin/mail", Summary: "Email (SMTP) settings", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			raw := a.SecretSetting("mail.smtp_url")
			masked := ""
			if u, err := url.Parse(raw); err == nil && raw != "" {
				if u.User != nil {
					u.User = url.UserPassword(u.User.Username(), "••••")
				}
				masked = u.String()
			}
			_, err := cfg()
			msg := ""
			if err != nil && raw != "" {
				msg = err.Error()
			}
			return map[string]any{"configured": err == nil, "smtpUrl": masked, "from": a.Setting("mail.from"), "problem": msg, "pinned": a.SettingFromEnv("mail.smtp_url")}, nil
		}})
	a.Handle(core.Op{Name: "admin.mail.update", Method: "PUT", Path: "/api/v1/admin/mail", Summary: "Set the SMTP server used to email invites and notifications", Access: core.AccessAdmin,
		Params: []core.Param{{Name: "smtpUrl", In: "body", Desc: "smtp://user:password@smtp.example.com:587 (STARTTLS) or smtps://...:465; empty turns email off"},
			{Name: "from", In: "body", Desc: "From address, e.g. Bunker <bunker@example.com>"}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ SmtpURL, From *string }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			if in.SmtpURL != nil && !strings.Contains(*in.SmtpURL, "••••") {
				if a.SettingFromEnv("mail.smtp_url") {
					return nil, core.Conflict("the SMTP URL is set by the server environment")
				}
				a.SetSecretSetting("mail.smtp_url", strings.TrimSpace(*in.SmtpURL))
			}
			if in.From != nil {
				a.SetSetting(r.Context(), "mail.from", strings.TrimSpace(*in.From))
			}
			a.Audit(r.Context(), "admin.config", 0, "mail", nil)
			if _, err := cfg(); err != nil && a.SecretSetting("mail.smtp_url") != "" {
				return nil, core.BadRequest(err.Error())
			}
			return map[string]any{"ok": true, "configured": Enabled()}, nil
		}})
	a.Handle(core.Op{Name: "admin.mail.test", Method: "POST", Path: "/api/v1/admin/mail/test", Summary: "Send a test email to yourself", Access: core.AccessAdmin,
		Fn: func(r *http.Request) (any, error) {
			u := core.MustUser(r)
			start := time.Now()
			err := deliver(Message{To: u.Email, Subject: "Bunker email works", Text: "This is a test from Bunker. Invites, password resets and notifications will now arrive by email."})
			if err != nil {
				return map[string]any{"ok": false, "error": err.Error()}, nil
			}
			return map[string]any{"ok": true, "ms": time.Since(start).Milliseconds()}, nil
		}})
	a.Handle(core.Op{Name: "me.email_notifications", Method: "PUT", Path: "/api/v1/me/email-notifications", Summary: "Turn email notifications on or off for yourself",
		Params: []core.Param{{Name: "enabled", In: "body", Type: "boolean", Required: true}},
		Fn: func(r *http.Request) (any, error) {
			var in struct{ Enabled bool }
			if err := core.Decode(r, &in); err != nil {
				return nil, err
			}
			off := 1
			if in.Enabled {
				off = 0
			}
			a.DB.ExecContext(r.Context(), `INSERT INTO mail_prefs(user_email,off) VALUES(?,?) ON CONFLICT(user_email) DO UPDATE SET off=excluded.off`, strings.ToLower(core.MustUser(r).Email), off)
			return map[string]any{"ok": true, "enabled": in.Enabled}, nil
		}})
	a.Handle(core.Op{Name: "me.email_notifications.get", Method: "GET", Path: "/api/v1/me/email-notifications", Summary: "Whether you get notifications by email",
		Fn: func(r *http.Request) (any, error) {
			return map[string]any{"available": Enabled(), "enabled": WantsEmail(r.Context(), core.MustUser(r).Email)}, nil
		}})
}

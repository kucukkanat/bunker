package mail

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// SMTP allows at most 998 bytes per line. A long notification body (pages
// may send 1000 characters) went out as one 8-bit line, which strict
// servers reject or break mid-character.
func TestEmailLinesStayShort(t *testing.T) {
	body := strings.Repeat("ünïcode words ", 300)
	raw, err := build("Bunker <b@corp.com>", Message{To: "a@corp.com", Subject: "Hi", Text: body, ButtonText: "Open", ButtonURL: "https://bunker.corp.com/s/x/?a=1&b=2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(raw, []byte("\r\n")) {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes", len(line))
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	p, err := mr.NextPart() // decodes quoted-printable
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(p)
	if !strings.Contains(string(text), strings.TrimSpace(body)) || !strings.Contains(string(text), "https://bunker.corp.com/s/x/?a=1&b=2") {
		t.Errorf("plain text part doesn't round-trip: %.200q", text)
	}
}

package helper

import (
	"embed"
	"strings"
	"testing"
)

//go:embed testdata/email_template.html
var emailTestTemplates embed.FS

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  John.Doe+tag@GoogleMail.com "); got != "johndoe@gmail.com" {
		t.Fatalf("unexpected Gmail normalization: %q", got)
	}
	if got := NormalizeEmail("invalid"); got != "invalid" {
		t.Fatalf("unexpected invalid email normalization: %q", got)
	}
}

func TestGenerateEmailBody(t *testing.T) {
	body, err := GenerateEmailBody(emailTestTemplates, "testdata/email_template.html", map[string]string{"Name": "Alice"})
	if err != nil || !strings.Contains(body, "Hello Alice") {
		t.Fatalf("unexpected rendered email: body=%q err=%v", body, err)
	}

	if _, err := GenerateEmailBody(emailTestTemplates, "missing.html", nil); err == nil {
		t.Fatal("expected missing template error")
	}
}

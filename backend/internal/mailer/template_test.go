package mailer

import (
	"strings"
	"testing"
)

func TestTemplates_Render_VerifyEmail(t *testing.T) {
	tmpl, err := LoadTemplate("verify_email")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := tmpl.Render(VerifyEmailData{DisplayName: "Alice", VerifyURL: "https://folio.app/auth/verify/tok123"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject == "" {
		t.Fatal("empty subject")
	}
	if !strings.Contains(msg.HTML, "tok123") || !strings.Contains(msg.Text, "tok123") {
		t.Fatalf("missing token: %+v", msg)
	}
	if !strings.Contains(msg.HTML, "Alice") {
		t.Fatal("html missing name")
	}
}

func TestTemplates_Render_PasswordReset(t *testing.T) {
	tmpl, err := LoadTemplate("password_reset")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := tmpl.Render(PasswordResetData{DisplayName: "Bob", ResetURL: "https://folio.app/reset/xyz", ExpiresIn: "30 minutes"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTML, "xyz") || !strings.Contains(msg.Text, "30 minutes") {
		t.Fatalf("missing fields")
	}
}

func TestTemplates_Render_UnknownTemplate(t *testing.T) {
	if _, err := LoadTemplate("nope"); err == nil {
		t.Fatal("expected error")
	}
}

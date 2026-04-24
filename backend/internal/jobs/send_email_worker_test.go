package jobs

import (
	"context"
	"testing"

	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/mailer"
)

func TestSendEmailWorker_RendersAndSends(t *testing.T) {
	rec := mailer.NewLogMailer(nil)
	w := NewSendEmailWorker(rec)
	err := w.Work(context.Background(), &river.Job[SendEmailArgs]{
		Args: SendEmailArgs{
			TemplateName: "verify_email",
			ToAddress:    "alice@example.com",
			Data: map[string]any{
				"DisplayName": "Alice",
				"VerifyURL":   "https://folio.app/auth/verify/t1",
			},
		},
	})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	sent := rec.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(sent))
	}
	if sent[0].To != "alice@example.com" || sent[0].Subject == "" || sent[0].HTML == "" || sent[0].Text == "" {
		t.Fatalf("bad message: %+v", sent[0])
	}
}

func TestSendEmailWorker_UnknownTemplateErrors(t *testing.T) {
	w := NewSendEmailWorker(mailer.NewLogMailer(nil))
	err := w.Work(context.Background(), &river.Job[SendEmailArgs]{
		Args: SendEmailArgs{TemplateName: "nope", ToAddress: "x@x.com"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

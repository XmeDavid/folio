package mailer_test

import (
	"context"
	"testing"

	"github.com/xmedavid/folio/backend/internal/mailer"
)

func TestLogMailer_RecordsSentMessages(t *testing.T) {
	m := mailer.NewLogMailer(nil)
	msg := mailer.Message{
		To:       "alice@example.com",
		Subject:  "Hello",
		Template: "test",
		Data:     map[string]any{"x": 1},
	}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := m.Sent()
	if len(got) != 1 {
		t.Fatalf("want 1 message, got %d", len(got))
	}
	if got[0].To != msg.To || got[0].Template != msg.Template {
		t.Fatalf("recorded message mismatch: %+v", got[0])
	}

	m.Reset()
	if len(m.Sent()) != 0 {
		t.Fatal("Reset did not clear sent slice")
	}
}

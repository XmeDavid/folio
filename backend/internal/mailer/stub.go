package mailer

import (
	"context"
	"log/slog"
	"sync"
)

// LogMailer records every message in memory and logs a one-line summary.
// Use it in dev, CI, and tests. Plan 3 replaces the default in prod with
// ResendMailer but keeps LogMailer wired in tests.
type LogMailer struct {
	Logger *slog.Logger
	mu     sync.Mutex
	sent   []Message
}

func NewLogMailer(l *slog.Logger) *LogMailer { return &LogMailer{Logger: l} }

func (m *LogMailer) Send(_ context.Context, msg Message) error {
	if msg.Template != "" && msg.HTML == "" && msg.Text == "" {
		if tmpl, err := LoadTemplate(msg.Template); err == nil {
			if rendered, err := tmpl.Render(msg.Data); err == nil {
				msg.Subject = rendered.Subject
				msg.HTML = rendered.HTML
				msg.Text = rendered.Text
			}
		}
	}
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	if m.Logger != nil {
		m.Logger.Info("mailer.send (stub)",
			"to", msg.To, "template", msg.Template, "subject", msg.Subject)
	}
	return nil
}

// Sent returns a snapshot of every message this mailer has recorded.
func (m *LogMailer) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.sent))
	copy(out, m.sent)
	return out
}

// Reset clears the recorded slice. Useful between test cases.
func (m *LogMailer) Reset() {
	m.mu.Lock()
	m.sent = nil
	m.mu.Unlock()
}

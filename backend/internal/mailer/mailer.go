// Package mailer defines the transactional email transport used by Folio.
//
// Plan 2 ships the interface + a LogMailer stub. Plan 3 replaces the stub
// with a Resend-backed implementation driven by River jobs. Plan 2's
// invite handler calls Send directly; plan 3 rewires it to enqueue.
package mailer

import "context"

// Message is the wire shape for a transactional email.
type Message struct {
	To       string // primary recipient (lowercase, normalized)
	Subject  string
	HTML     string
	Text     string
	ReplyTo  string
	Template string         // template name; mailer looks it up
	Data     map[string]any // template data
	WorkspaceID string         // optional — for audit / inbound routing
}

// Mailer sends transactional email. Implementations MUST be safe for
// concurrent use; they MAY batch, retry, or buffer internally.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

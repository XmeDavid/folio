package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ResendMailer struct {
	apiKey  string
	from    string
	baseURL string
	client  *http.Client
}

type Option func(*ResendMailer)

func WithBaseURL(u string) Option { return func(m *ResendMailer) { m.baseURL = u } }

func WithHTTPClient(c *http.Client) Option { return func(m *ResendMailer) { m.client = c } }

func NewResendMailer(apiKey, from string, opts ...Option) *ResendMailer {
	m := &ResendMailer{
		apiKey:  apiKey,
		from:    from,
		baseURL: "https://api.resend.com",
		client:  &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

func (m *ResendMailer) Send(ctx context.Context, msg Message) error {
	if msg.Template != "" && msg.HTML == "" && msg.Text == "" {
		tmpl, err := LoadTemplate(msg.Template)
		if err != nil {
			return err
		}
		rendered, err := tmpl.Render(msg.Data)
		if err != nil {
			return err
		}
		msg.Subject = rendered.Subject
		msg.HTML = rendered.HTML
		msg.Text = rendered.Text
	}
	body, err := json.Marshal(resendRequest{
		From:    m.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		HTML:    msg.HTML,
		Text:    msg.Text,
		ReplyTo: msg.ReplyTo,
	})
	if err != nil {
		return fmt.Errorf("resend: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: build: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("resend: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend: %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

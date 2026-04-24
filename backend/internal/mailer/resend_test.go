package mailer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendMailer_Send_PostsToResendAPI(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"id":"abc123"}`))
	}))
	defer srv.Close()

	m := NewResendMailer("key_test", "Folio <no-reply@folio.app>", WithBaseURL(srv.URL))
	err := m.Send(context.Background(), Message{To: "user@example.com", Subject: "Verify your email", HTML: "<p>hi</p>", Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("missing bearer auth: %q", gotAuth)
	}
	if gotBody["from"] != "Folio <no-reply@folio.app>" || gotBody["subject"] != "Verify your email" || gotBody["html"] != "<p>hi</p>" {
		t.Fatalf("bad body: %+v", gotBody)
	}
	to, ok := gotBody["to"].([]any)
	if !ok || len(to) != 1 || to[0] != "user@example.com" {
		t.Fatalf("bad to: %v", gotBody["to"])
	}
}

func TestResendMailer_Send_Returns4xxAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad"}`))
	}))
	defer srv.Close()

	m := NewResendMailer("k", "f@x.co", WithBaseURL(srv.URL))
	if err := m.Send(context.Background(), Message{To: "u@x.co", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("expected error")
	}
}

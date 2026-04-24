package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEmailFlowRateLimitsVerifyResend(t *testing.T) {
	limits := newEmailFlowRateLimits()
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	limits.verifyResendMinute.now = func() time.Time { return now }
	limits.verifyResendHour.now = func() time.Time { return now }
	userID := uuid.New()

	if !limits.allowVerifyResend(userID) {
		t.Fatalf("first resend should be allowed")
	}
	if limits.allowVerifyResend(userID) {
		t.Fatalf("second resend inside one minute should be rejected")
	}

	for i := 0; i < 4; i++ {
		now = now.Add(time.Minute + time.Second)
		if !limits.allowVerifyResend(userID) {
			t.Fatalf("resend %d after minute window should be allowed", i+2)
		}
	}

	now = now.Add(time.Minute + time.Second)
	if limits.allowVerifyResend(userID) {
		t.Fatalf("sixth resend inside one hour should be rejected")
	}
}

func TestEmailFlowRateLimitsPasswordReset(t *testing.T) {
	limits := newEmailFlowRateLimits()

	for i := 0; i < 3; i++ {
		if !limits.allowPasswordReset("203.0.113.10", "ALICE@example.com") {
			t.Fatalf("password reset attempt %d should be allowed", i+1)
		}
	}
	if limits.allowPasswordReset("203.0.113.10", "alice@example.com") {
		t.Fatalf("fourth password reset for same IP/email should be rejected")
	}
	if limits.allowPasswordReset("203.0.113.11", "alice@example.com") {
		t.Fatalf("fourth password reset for same email should be rejected across IPs")
	}
	if !limits.allowPasswordReset("203.0.113.11", "other@example.com") {
		t.Fatalf("different IP/email should have independent budget")
	}
}

func TestEmailFlowRateLimitsEmailChange(t *testing.T) {
	limits := newEmailFlowRateLimits()
	userID := uuid.New()

	for i := 0; i < 3; i++ {
		if !limits.allowEmailChange(userID) {
			t.Fatalf("email change attempt %d should be allowed", i+1)
		}
	}
	if limits.allowEmailChange(userID) {
		t.Fatalf("fourth email change attempt should be rejected")
	}
}

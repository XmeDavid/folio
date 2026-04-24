package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type emailFlowRateLimits struct {
	verifyResendMinute *tokenBucket
	verifyResendHour   *tokenBucket
	passwordResetIP    *tokenBucket
	passwordResetEmail *tokenBucket
	emailChange        *tokenBucket
}

func newEmailFlowRateLimits() *emailFlowRateLimits {
	return &emailFlowRateLimits{
		verifyResendMinute: newTokenBucket(1, time.Minute),
		verifyResendHour:   newTokenBucket(5, time.Hour),
		passwordResetIP:    newTokenBucket(3, time.Hour),
		passwordResetEmail: newTokenBucket(3, time.Hour),
		emailChange:        newTokenBucket(3, time.Hour),
	}
}

func (l *emailFlowRateLimits) allowVerifyResend(userID uuid.UUID) bool {
	key := fmt.Sprintf("verify-resend:user:%s", userID)
	return l.verifyResendMinute.take(key) && l.verifyResendHour.take(key)
}

func (l *emailFlowRateLimits) allowPasswordReset(ip, email string) bool {
	email = normalizeEmailLimitKey(email)
	if email == "" {
		return l.passwordResetIP.take("password-reset:ip:" + ip)
	}
	return l.passwordResetIP.take("password-reset:ip:"+ip) &&
		l.passwordResetEmail.take("password-reset:email:"+email)
}

func (l *emailFlowRateLimits) allowEmailChange(userID uuid.UUID) bool {
	return l.emailChange.take(fmt.Sprintf("email-change:user:%s", userID))
}

func normalizeEmailLimitKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

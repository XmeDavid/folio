package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
)

type MFAChallenge struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	IP            net.IP
	UserAgent     string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	ConsumedAt    *time.Time
	Attempts      int
	WebAuthnState []byte
}

var (
	ErrChallengeNotFound = errors.New("mfa challenge not found")
	ErrChallengeExpired  = errors.New("mfa challenge expired")
	ErrChallengeBinding  = errors.New("mfa challenge ip mismatch")
	ErrChallengeConsumed = errors.New("mfa challenge already consumed")
)

func InsertMFAChallenge(ctx context.Context, db *pgxpool.Pool, c MFAChallenge) error {
	// auth_mfa_challenges.ip is NOT NULL; callers always supply an IP.
	// netIPToAddr returns *netip.Addr; dereference to the non-nullable value.
	ip := netIPToAddrVal(c.IP)
	return dbq.New(db).InsertMFAChallenge(ctx, dbq.InsertMFAChallengeParams{
		ID:            c.ID,
		UserID:        c.UserID,
		Ip:            ip,
		UserAgent:     c.UserAgent,
		CreatedAt:     c.CreatedAt,
		ExpiresAt:     c.ExpiresAt,
		WebauthnState: c.WebAuthnState,
	})
}

func LoadAndBindMFAChallenge(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, ip net.IP, ua string, now time.Time) (MFAChallenge, error) {
	row, err := dbq.New(db).GetMFAChallengeByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return MFAChallenge{}, ErrChallengeNotFound
	}
	if err != nil {
		return MFAChallenge{}, err
	}
	c := MFAChallenge{
		ID:            row.ID,
		UserID:        row.UserID,
		IP:            net.ParseIP(row.Ip),
		UserAgent:     row.UserAgent,
		CreatedAt:     row.CreatedAt,
		ExpiresAt:     row.ExpiresAt,
		ConsumedAt:    row.ConsumedAt,
		Attempts:      int(row.Attempts),
		WebAuthnState: row.WebauthnState,
	}
	if c.ConsumedAt != nil {
		return c, ErrChallengeConsumed
	}
	if now.After(c.ExpiresAt) {
		return c, ErrChallengeExpired
	}
	// Bind to IP only. UA strict-equality was rejected legitimate completions
	// when the browser auto-updated mid-flow; IP binding is the load-bearing
	// half of the binding check.
	if !c.IP.Equal(ip) {
		return c, ErrChallengeBinding
	}
	return c, nil
}

func ConsumeMFAChallenge(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) error {
	n, err := dbq.New(tx).ConsumeMFAChallenge(ctx, dbq.ConsumeMFAChallengeParams{
		ID:         id,
		ConsumedAt: &now,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrChallengeConsumed
	}
	return nil
}

func BumpAttempts(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, now time.Time) (int, error) {
	attempts, err := dbq.New(db).BumpMFAChallengeAttempts(ctx, dbq.BumpMFAChallengeAttemptsParams{
		ID:         id,
		ConsumedAt: &now,
	})
	return int(attempts), err
}

func UpdateWebAuthnState(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, state any) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return dbq.New(db).UpdateMFAChallengeWebAuthnState(ctx, dbq.UpdateMFAChallengeWebAuthnStateParams{
		ID:            id,
		WebauthnState: b,
	})
}

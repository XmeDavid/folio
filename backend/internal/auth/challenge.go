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
	ErrChallengeBinding  = errors.New("mfa challenge ip/ua mismatch")
	ErrChallengeConsumed = errors.New("mfa challenge already consumed")
)

func InsertMFAChallenge(ctx context.Context, db *pgxpool.Pool, c MFAChallenge) error {
	_, err := db.Exec(ctx, `
		insert into auth_mfa_challenges
			(id, user_id, ip, user_agent, created_at, expires_at, webauthn_state)
		values ($1, $2, $3, $4, $5, $6, $7)
	`, c.ID, c.UserID, ipString(c.IP), c.UserAgent, c.CreatedAt, c.ExpiresAt, c.WebAuthnState)
	return err
}

func LoadAndBindMFAChallenge(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, ip net.IP, ua string, now time.Time) (MFAChallenge, error) {
	var c MFAChallenge
	var ipStr string
	err := db.QueryRow(ctx, `
		select id, user_id, ip::text, user_agent, created_at, expires_at, consumed_at, attempts, coalesce(webauthn_state, '{}'::jsonb)
		from auth_mfa_challenges
		where id = $1
	`, id).Scan(&c.ID, &c.UserID, &ipStr, &c.UserAgent, &c.CreatedAt, &c.ExpiresAt, &c.ConsumedAt, &c.Attempts, &c.WebAuthnState)
	if errors.Is(err, pgx.ErrNoRows) {
		return MFAChallenge{}, ErrChallengeNotFound
	}
	if err != nil {
		return MFAChallenge{}, err
	}
	c.IP = net.ParseIP(ipStr)
	if c.ConsumedAt != nil {
		return c, ErrChallengeConsumed
	}
	if now.After(c.ExpiresAt) {
		return c, ErrChallengeExpired
	}
	if !c.IP.Equal(ip) || c.UserAgent != ua {
		return c, ErrChallengeBinding
	}
	return c, nil
}

func ConsumeMFAChallenge(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) error {
	ct, err := tx.Exec(ctx, `
		update auth_mfa_challenges set consumed_at = $2
		where id = $1 and consumed_at is null
	`, id, now)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrChallengeConsumed
	}
	return nil
}

func BumpAttempts(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, now time.Time) (int, error) {
	var attempts int
	err := db.QueryRow(ctx, `
		update auth_mfa_challenges
		set attempts = attempts + 1,
		    consumed_at = case when attempts + 1 >= 10 then $2 else consumed_at end
		where id = $1
		returning attempts
	`, id, now).Scan(&attempts)
	return attempts, err
}

func UpdateWebAuthnState(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, state any) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `update auth_mfa_challenges set webauthn_state = $2 where id = $1`, id, b)
	return err
}

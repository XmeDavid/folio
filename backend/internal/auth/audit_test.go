package auth

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/xmedavid/folio/backend/internal/uuidx"
	"github.com/xmedavid/folio/backend/internal/testdb"
)

// TestWriteAuditTx_nilIPStoresNull is a regression test: a nil net.IP must
// flow through ipString -> pgx -> audit_events.ip as SQL NULL. Previously
// ipString returned "" which Postgres rejects on `inet` columns.
func TestWriteAuditTx_nilIPStoresNull(t *testing.T) {
	testdb.WithTx(t, func(ctx context.Context, tx pgx.Tx) {
		// Seed a user so actor_user_id FK is satisfiable.
		userID := uuidx.New()
		if _, err := tx.Exec(ctx, `
			insert into users (id, email, password_hash, display_name)
			values ($1, $2, 'x', 'Nil IP')
		`, userID, "nilip+"+userID.String()+"@example.com"); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		// Write with nil IP — must not error, must store NULL.
		if err := writeAuditTx(ctx, tx, nil, &userID, "test.nil_ip", "user", userID, nil, nil, nil, ""); err != nil {
			t.Fatalf("writeAuditTx nil IP: %v", err)
		}

		// Confirm a row exists and ip column is NULL.
		var ipNull bool
		if err := tx.QueryRow(ctx,
			`select ip is null from audit_events where action = 'test.nil_ip' and actor_user_id = $1`,
			userID,
		).Scan(&ipNull); err != nil {
			t.Fatalf("select: %v", err)
		}
		if !ipNull {
			t.Fatalf("expected audit_events.ip to be NULL for nil-IP write")
		}
	})
}

// TestWriteAuditTx_valueIPStoresAddress verifies the non-nil path still writes the
// IP address text form.
func TestWriteAuditTx_valueIPStoresAddress(t *testing.T) {
	testdb.WithTx(t, func(ctx context.Context, tx pgx.Tx) {
		userID := uuidx.New()
		if _, err := tx.Exec(ctx, `
			insert into users (id, email, password_hash, display_name)
			values ($1, $2, 'x', 'IP')
		`, userID, "ip+"+userID.String()+"@example.com"); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		ip := net.ParseIP("203.0.113.42")
		if err := writeAuditTx(ctx, tx, nil, &userID, "test.value_ip", "user", userID, nil, nil, ip, ""); err != nil {
			t.Fatalf("writeAuditTx value IP: %v", err)
		}
		var got string
		if err := tx.QueryRow(ctx,
			`select host(ip) from audit_events where action = 'test.value_ip' and actor_user_id = $1`,
			userID,
		).Scan(&got); err != nil {
			t.Fatalf("select: %v", err)
		}
		if got != "203.0.113.42" {
			t.Errorf("ip = %q, want 203.0.113.42", got)
		}
	})
}

// Reference uuid import so the file compiles even if uuid/uuidx usage shifts.
var _ = uuid.Nil

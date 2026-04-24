package auth

import (
	"context"
	"fmt"
	"net"

	"github.com/google/uuid"
)

// Logout deletes the session row. The cookie is cleared by the handler.
func (s *Service) Logout(ctx context.Context, sessionID string, userID uuid.UUID, ip net.IP, ua string) error {
	_, err := s.pool.Exec(ctx, `delete from sessions where id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	s.logAuditDirect(ctx, nil, &userID, "user.logout", "user", userID, ip, ua)
	return nil
}

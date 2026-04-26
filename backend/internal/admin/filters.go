package admin

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Pagination struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"nextCursor,omitempty"`
}

type AdminListFilter struct {
	Limit  int
	Cursor string
}

func (f AdminListFilter) Normalize() AdminListFilter {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	return f
}

type WorkspaceListFilter struct {
	AdminListFilter
	Search         string
	IncludeDeleted bool
}

type UserListFilter struct {
	AdminListFilter
	Search      string
	IsAdminOnly bool
}

type AuditFilter struct {
	AdminListFilter
	ActorUserID *uuid.UUID
	WorkspaceID    *uuid.UUID
	Action      string
	Since       *time.Time
	Until       *time.Time
}

type JobFilter struct {
	AdminListFilter
	State string
	Kind  string
}

func encodeCursor(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCursor(s string, into any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

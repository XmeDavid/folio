// Package identity owns read/write queries for users, workspaces, memberships,
// and (in plan 2) invites. It is intentionally free of credential code —
// password hashing, session cookies, and HTTP middleware live in
// backend/internal/auth.
package identity

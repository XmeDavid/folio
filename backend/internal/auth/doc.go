// Package auth will house user auth: session cookies, Argon2id password hashing,
// and WebAuthn/passkey registration & login.
//
// Planned layout:
//
//	session.go   — cookie store, CSRF, middleware
//	password.go  — Argon2id hash/verify
//	webauthn.go  — passkey ceremonies
package auth

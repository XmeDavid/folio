-- name: InsertTestWorkspace :exec
INSERT INTO workspaces (id, name, slug, base_currency, cycle_anchor_day, locale, timezone)
VALUES (@id, @name, @slug, 'CHF', 1, 'en', 'UTC');

-- name: InsertTestUser :exec
INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
VALUES (@id, @email, @display_name, '$argon2id$stub', @email_verified_at);

-- name: UpdateSessionReauthByID :exec
UPDATE sessions SET reauth_at = @reauth_at WHERE id = @id;

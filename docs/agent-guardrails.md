# Agent Guardrails

Use these rules for architecture-sensitive Folio work.

## Contract

- `openapi/openapi.yaml` changes with every API route or payload change.
- Run `make openapi` after contract edits.
- Workspace-scoped application routes use `/api/v1/t/{workspaceId}/...`.
- Do not add `X-Workspace-ID` or other ambient-workspace APIs.
- Generated files are regenerated, not hand-edited.

## Backend

- Workspace-scoped queries must include `workspace_id` or go through middleware
  and service code that enforces membership.
- Reads inside an open transaction must use the same transaction query surface;
  avoid `dbq.New(s.pool)` for tx-local rechecks or dedupe.
- Durable security mutations need fresh reauth when a stolen session could do
  lasting damage.
- Global invariants, such as keeping at least one admin, need a DB lock,
  constraint, or serializable retry path.
- Prefer sqlc for stable queries. Keep dynamic PATCH SQL narrow and tested.

## Frontend

- Avoid turning route files into multi-workflow containers. Extract components
  and hooks when a page grows new responsibilities.
- Use shared API helpers; use `json:` for JSON bodies.
- Use Radix-backed primitives for dialogs and menus that need focus handling.
- Important forms should use react-hook-form and zod unless nearby code has a
  deliberate different pattern.

## Validation

Run targeted tests for the changed area and prefer this full set before
hand-off:

```bash
make guardrails
cd backend && go test ./...
cd web && pnpm typecheck && pnpm lint && pnpm test
```

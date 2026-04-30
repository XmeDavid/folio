# OpenAPI

`openapi.yaml` is the source of truth for API contracts.

## Regenerate clients

From the repo root:

```bash
make openapi
```

This runs:

1. **Go server stubs** → `backend/internal/api/api.gen.go`
2. **TypeScript types** → `web/lib/api/schema.d.ts`

## Editing guidelines

- Never hand-edit generated files.
- Add or update `openapi/openapi.yaml` in the same change as any API route
  shape or payload change, then run `make openapi`.
- Workspace-scoped application routes use `/api/v1/t/{workspaceId}/...`;
  do not add new `X-Workspace-ID` header-scoped routes.
- Use `$ref` aggressively; avoid duplicating schemas.
- Path and component names use `camelCase` in JSON, even though Go types will be generated as Go-idiomatic `PascalCase`.
- Money fields are always `string` in the wire format, carrying a decimal representation (e.g. `"1234.56"`). This avoids JS number precision issues and matches the Go `decimal.Decimal` type.

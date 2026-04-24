// Typed Folio API client. Generated types live next to this file.
//
// Auth is not real yet; tenant-scoped routes use a temporary X-Tenant-ID header.
// The helpers below take an explicit tenantId argument so that callers can never
// accidentally fall back to an ambient "current" tenant.

import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";

const TENANT_HEADER = "X-Tenant-ID";

const baseUrl =
  typeof window === "undefined"
    ? (process.env.API_URL ?? "http://localhost:8080")
    : ""; // browser uses Next rewrite

export const api = createClient<paths>({ baseUrl });

// Bootstrapping the tenant/user is a public route; keep this path explicit.
export async function bootstrapTenant(body: BootstrapInput) {
  const { data, error, response } = await api.POST(
    "/api/v1/onboarding/bootstrap",
    { body }
  );
  if (error || !data) {
    throw new ApiError((response as Response)?.status ?? 0, error);
  }
  return data;
}

export async function fetchMe(tenantId: string) {
  const { data, error, response } = await api.GET("/api/v1/me", {
    headers: tenantHeaders(tenantId),
  });
  if (error || !data) {
    throw new ApiError((response as Response)?.status ?? 0, error);
  }
  return data;
}

export async function fetchAccounts(
  tenantId: string,
  opts: { includeArchived?: boolean } = {}
) {
  const { data, error, response } = await api.GET("/api/v1/accounts", {
    params: { query: { includeArchived: opts.includeArchived ?? false } },
    headers: tenantHeaders(tenantId),
  });
  if (error || !data) {
    throw new ApiError((response as Response)?.status ?? 0, error);
  }
  return data;
}

export async function createAccount(
  tenantId: string,
  body: AccountCreateInput
) {
  const { data, error, response } = await api.POST("/api/v1/accounts", {
    body,
    headers: tenantHeaders(tenantId),
  });
  if (error || !data) {
    throw new ApiError((response as Response)?.status ?? 0, error);
  }
  return data;
}

export async function fetchTransactions(
  tenantId: string,
  query: paths["/api/v1/transactions"]["get"]["parameters"]["query"] = {}
) {
  const { data, error, response } = await api.GET("/api/v1/transactions", {
    params: { query },
    headers: tenantHeaders(tenantId),
  });
  if (error || !data) {
    throw new ApiError((response as Response)?.status ?? 0, error);
  }
  return data;
}

export async function createTransaction(
  tenantId: string,
  body: TransactionCreateInput
) {
  const { data, error, response } = await api.POST("/api/v1/transactions", {
    body,
    headers: tenantHeaders(tenantId),
  });
  if (error || !data) {
    throw new ApiError((response as Response)?.status ?? 0, error);
  }
  return data;
}

function tenantHeaders(tenantId: string): Record<string, string> {
  return { [TENANT_HEADER]: tenantId };
}

export type BootstrapInput = components["schemas"]["BootstrapInput"];
export type BootstrapResult = components["schemas"]["BootstrapResult"];
export type MeResult = components["schemas"]["MeResult"];
export type Tenant = components["schemas"]["Tenant"];
export type User = components["schemas"]["User"];
export type Account = components["schemas"]["Account"];
export type AccountKind = components["schemas"]["AccountKind"];
export type AccountCreateInput = components["schemas"]["AccountCreateInput"];
export type Transaction = components["schemas"]["Transaction"];
export type TransactionStatus = components["schemas"]["TransactionStatus"];
export type TransactionCreateInput =
  components["schemas"]["TransactionCreateInput"];
export type ErrorBody = { error?: string; code?: string; details?: unknown };

export class ApiError extends Error {
  status: number;
  body: ErrorBody | undefined;
  constructor(status: number, body: unknown) {
    const b = (body as ErrorBody) ?? undefined;
    super(b?.error || `Request failed (${status})`);
    this.status = status;
    this.body = b;
  }
}

// Middleware hook is exported for future session auth wiring.
export function installAuthMiddleware(mw: Middleware) {
  api.use(mw);
}

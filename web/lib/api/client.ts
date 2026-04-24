// Typed Folio API client. Generated types live next to this file in schema.d.ts.
//
// Every request includes:
//   - credentials: "include" so the session cookie is sent
//   - X-Folio-Request: 1 to satisfy the backend CSRF header check
//
// Tenant-scoped resources live under /api/v1/t/{tenantId}/…. Each helper takes
// an explicit tenantId as the first argument so callers can never fall back to
// an ambient "current" tenant.

import type { components, paths } from "./schema";
import type { Me } from "@/lib/hooks/use-identity";

const CSRF_HEADER_NAME = "X-Folio-Request";
const CSRF_HEADER_VALUE = "1";

const baseUrl =
  typeof window === "undefined"
    ? (process.env.API_URL ?? "http://localhost:8080")
    : ""; // browser uses Next rewrite

function defaultHeaders(extra?: HeadersInit): HeadersInit {
  const base: Record<string, string> = {
    [CSRF_HEADER_NAME]: CSRF_HEADER_VALUE,
  };
  if (!extra) return base;
  return { ...base, ...(extra as Record<string, string>) };
}

async function parseError(res: Response): Promise<ApiError> {
  let body: unknown;
  try {
    body = await res.json();
  } catch {
    body = undefined;
  }
  return new ApiError(res.status, body);
}

async function request<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {},
): Promise<T> {
  const { json, headers, ...rest } = init;
  const mergedHeaders: Record<string, string> = {
    ...(defaultHeaders(headers) as Record<string, string>),
  };
  let body = rest.body;
  if (json !== undefined) {
    mergedHeaders["Content-Type"] = "application/json";
    body = JSON.stringify(json);
  }
  const res = await fetch(`${baseUrl}${path}`, {
    ...rest,
    credentials: "include",
    headers: mergedHeaders,
    body,
  });
  if (!res.ok) {
    throw await parseError(res);
  }
  // 204 No Content
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

// ---------------------------------------------------------------------------
// Identity
// ---------------------------------------------------------------------------

export async function fetchMe(): Promise<Me> {
  return request<Me>("/api/v1/me", { method: "GET" });
}

// ---------------------------------------------------------------------------
// Tenant
// ---------------------------------------------------------------------------

export type TenantPatchInput = {
  name?: string;
  slug?: string;
  baseCurrency?: string;
  cycleAnchorDay?: number;
};

export type TenantRow = {
  id: string;
  name: string;
  slug: string;
  baseCurrency: string;
  cycleAnchorDay: number;
  locale: string;
  timezone: string;
  deletedAt?: string;
  createdAt: string;
};

export async function patchTenant(
  tenantId: string,
  body: TenantPatchInput,
): Promise<TenantRow> {
  return request<TenantRow>(`/api/v1/t/${tenantId}`, {
    method: "PATCH",
    json: body,
  });
}

export async function deleteTenant(tenantId: string): Promise<void> {
  return request<void>(`/api/v1/t/${tenantId}`, { method: "DELETE" });
}

export async function restoreTenant(tenantId: string): Promise<TenantRow> {
  return request<TenantRow>(`/api/v1/t/${tenantId}/restore`, {
    method: "POST",
  });
}

/**
 * toApiError parses a Response into an ApiError. Useful for callers that use
 * raw fetch() rather than the request<T> helper.
 */
export async function toApiError(res: Response): Promise<ApiError> {
  return parseError(res);
}

// ---------------------------------------------------------------------------
// Members & invites
// ---------------------------------------------------------------------------

export type MemberRole = "owner" | "member";

export type MemberWithUser = {
  tenantId: string;
  userId: string;
  role: MemberRole;
  createdAt: string;
  email: string;
  displayName: string;
};

export type PendingInvite = {
  id: string;
  email: string;
  role: MemberRole;
  invitedByUserId: string;
  invitedAt: string;
  expiresAt: string;
};

export type MembersResponse = {
  members: MemberWithUser[];
  pendingInvites: PendingInvite[];
};

export async function getMembers(tenantId: string): Promise<MembersResponse> {
  return request<MembersResponse>(`/api/v1/t/${tenantId}/members`, {
    method: "GET",
  });
}

export async function patchMember(
  tenantId: string,
  userId: string,
  role: MemberRole,
): Promise<MemberWithUser> {
  return request<MemberWithUser>(
    `/api/v1/t/${tenantId}/members/${userId}`,
    {
      method: "PATCH",
      json: { role },
    },
  );
}

export async function removeMember(
  tenantId: string,
  userId: string,
): Promise<void> {
  return request<void>(`/api/v1/t/${tenantId}/members/${userId}`, {
    method: "DELETE",
  });
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

export async function fetchAccounts(
  tenantId: string,
  opts: { includeArchived?: boolean } = {},
): Promise<Account[]> {
  const qs = opts.includeArchived ? "?includeArchived=true" : "";
  return request<Account[]>(`/api/v1/t/${tenantId}/accounts${qs}`, {
    method: "GET",
  });
}

export async function createAccount(
  tenantId: string,
  body: AccountCreateInput,
): Promise<Account> {
  return request<Account>(`/api/v1/t/${tenantId}/accounts`, {
    method: "POST",
    json: body,
  });
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

type TransactionsQuery = {
  accountId?: string;
  from?: string;
  to?: string;
  status?: TransactionStatus;
  limit?: number;
};

function buildQuery(q?: Record<string, unknown>): string {
  if (!q) return "";
  const parts: string[] = [];
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue;
    parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

export async function fetchTransactions(
  tenantId: string,
  query: TransactionsQuery = {},
): Promise<Transaction[]> {
  return request<Transaction[]>(
    `/api/v1/t/${tenantId}/transactions${buildQuery(query)}`,
    { method: "GET" },
  );
}

export async function createTransaction(
  tenantId: string,
  body: TransactionCreateInput,
): Promise<Transaction> {
  return request<Transaction>(`/api/v1/t/${tenantId}/transactions`, {
    method: "POST",
    json: body,
  });
}

// ---------------------------------------------------------------------------
// Types re-exported from the OpenAPI-generated schema. Schema regeneration is
// not part of this change; types that reference removed endpoints still live
// in schema.d.ts until a regeneration pass syncs with the v2 spec.
// ---------------------------------------------------------------------------

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

// `paths` is re-exported so callers can still reference path-derived generics.
// Kept as a type-only re-export to avoid importing unused runtime code.
export type { paths };

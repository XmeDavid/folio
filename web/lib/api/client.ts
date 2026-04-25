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

async function uploadRequest<T>(path: string, form: FormData): Promise<T> {
  const res = await fetch(`${baseUrl}${path}`, {
    method: "POST",
    credentials: "include",
    headers: defaultHeaders(),
    body: form,
  });
  if (!res.ok) {
    throw await parseError(res);
  }
  return (await res.json()) as T;
}

// ---------------------------------------------------------------------------
// Identity
// ---------------------------------------------------------------------------

export async function fetchMe(): Promise<Me> {
  return request<Me>("/api/v1/me", { method: "GET" });
}

export type MFAStatus = {
  totpEnrolled: boolean;
  passkeyCount: number;
  remainingRecoveryCodes: number;
};

export type TOTPSetup = {
  secret: string;
  uri: string;
  qrCodeBase64: string;
};

export async function fetchMFAStatus(): Promise<MFAStatus> {
  return request<MFAStatus>("/api/v1/me/mfa", { method: "GET" });
}

export async function enrollTOTP(): Promise<TOTPSetup> {
  return request<TOTPSetup>("/api/v1/me/mfa/totp/enroll", { method: "POST" });
}

export async function confirmTOTP(code: string): Promise<{ recoveryCodes: string[] }> {
  return request<{ recoveryCodes: string[] }>("/api/v1/me/mfa/totp/confirm", {
    method: "POST",
    json: { code },
  });
}

export async function beginPasskeyEnrollment(): Promise<{ options: unknown; session: string }> {
  return request<{ options: unknown; session: string }>("/api/v1/me/mfa/passkeys/begin", {
    method: "POST",
  });
}

export async function completePasskeyEnrollment(
  session: string,
  credential: unknown,
  label = "Passkey",
): Promise<void> {
  return request<void>("/api/v1/me/mfa/passkeys/complete", {
    method: "POST",
    json: { session, label, credential },
  });
}

export async function regenerateRecoveryCodes(): Promise<{ recoveryCodes: string[] }> {
  return request<{ recoveryCodes: string[] }>("/api/v1/me/mfa/recovery-codes", {
    method: "POST",
  });
}

export async function reauth(password: string, code?: string): Promise<void> {
  return request<void>("/api/v1/auth/reauth", {
    method: "POST",
    json: { password, code },
  });
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

export type InviteCreateInput = {
  email: string;
  role: MemberRole;
};

export async function createInvite(
  tenantId: string,
  body: InviteCreateInput,
): Promise<PendingInvite> {
  return request<PendingInvite>(`/api/v1/t/${tenantId}/invites`, {
    method: "POST",
    json: body,
  });
}

export async function revokeInvite(
  tenantId: string,
  inviteId: string,
): Promise<void> {
  return request<void>(`/api/v1/t/${tenantId}/invites/${inviteId}`, {
    method: "DELETE",
  });
}

export type InvitePreview = {
  tenantId: string;
  tenantName: string;
  tenantSlug: string;
  inviterDisplayName: string;
  email: string;
  role: MemberRole;
  expiresAt: string;
};

export type InviteAcceptResponse = {
  tenantId: string;
  userId: string;
  role: MemberRole;
  createdAt: string;
};

export async function previewInvite(token: string): Promise<InvitePreview> {
  return request<InvitePreview>(
    `/api/v1/auth/invites/${encodeURIComponent(token)}`,
    { method: "GET" },
  );
}

export async function acceptInvite(
  token: string,
): Promise<InviteAcceptResponse> {
  return request<InviteAcceptResponse>(
    `/api/v1/auth/invites/${encodeURIComponent(token)}/accept`,
    { method: "POST" },
  );
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

export type AccountPatchInput = {
  name?: string;
  nickname?: string | null;
  institution?: string | null;
  includeInNetworth?: boolean;
  includeInSavingsRate?: boolean;
  closeDate?: string | null;
  archived?: boolean;
};

export async function updateAccount(
  tenantId: string,
  accountId: string,
  body: AccountPatchInput,
): Promise<Account> {
  return request<Account>(`/api/v1/t/${tenantId}/accounts/${accountId}`, {
    method: "PATCH",
    json: body,
  });
}

export async function deleteAccount(
  tenantId: string,
  accountId: string,
): Promise<void> {
  return request<void>(`/api/v1/t/${tenantId}/accounts/${accountId}`, {
    method: "DELETE",
  });
}

export type ImportPreviewRow = {
  bookedAt: string;
  amount: string;
  currency: string;
  description: string;
};

export type ImportConflictPreview = {
  incoming: ImportPreviewRow;
  existing: ImportPreviewRow;
};

export type ImportPreview = {
  profile: string;
  institution?: string;
  accountHint?: string;
  suggestedName?: string;
  suggestedKind?: AccountKind;
  suggestedCurrency?: string;
  suggestedOpenDate?: string;
  transactionCount: number;
  dateFrom?: string;
  dateTo?: string;
  sampleTransactions: ImportPreviewRow[];
  warnings?: string[];
  fileToken: string;
  fileName?: string;
  fileHash: string;
  existingAccountId?: string;
  duplicateCount: number;
  conflictCount: number;
  importableCount: number;
  conflictTransactions?: ImportConflictPreview[];
  currencyGroups?: ImportCurrencyGroup[];
};

export type ImportCurrencyGroup = {
  currency: string;
  sourceKey?: string;
  suggestedName: string;
  suggestedKind: AccountKind;
  suggestedOpenDate?: string;
  transactionCount: number;
  dateFrom?: string;
  dateTo?: string;
  existingAccountId?: string;
  existingAccountName?: string;
  candidateAccounts?: ImportAccountCandidate[];
  action: "create_account" | "import_to_account";
  importableCount: number;
  duplicateCount: number;
  conflictCount: number;
  sampleTransactions: ImportPreviewRow[];
  conflictTransactions?: ImportConflictPreview[];
};

export type ImportAccountCandidate = {
  id: string;
  name: string;
  currency: string;
  institution?: string;
  importableCount: number;
  duplicateCount: number;
  conflictCount: number;
  conflictTransactions?: ImportConflictPreview[];
};

export type ImportPlanGroup = {
  currency: string;
  sourceKey?: string;
  action: "create_account" | "import_to_account";
  accountId?: string;
  name?: string;
  kind?: AccountKind;
  openDate?: string;
  openingBalance?: string;
  openingBalanceDate?: string;
};

export type ImportApplyResult = {
  batchId: string;
  insertedCount: number;
  duplicateCount: number;
  conflictCount: number;
  transactionIds: string[];
  conflicts?: ImportConflictPreview[];
};

export async function previewAccountImport(
  tenantId: string,
  file: File,
  accountId?: string,
): Promise<ImportPreview> {
  const form = new FormData();
  form.append("file", file);
  const path = accountId
    ? `/api/v1/t/${tenantId}/accounts/${accountId}/imports/preview`
    : `/api/v1/t/${tenantId}/accounts/import-preview`;
  return uploadRequest<ImportPreview>(path, form);
}

export async function applyAccountImport(
  tenantId: string,
  accountId: string,
  fileToken: string,
  currency?: string,
): Promise<ImportApplyResult> {
  return request<ImportApplyResult>(
    `/api/v1/t/${tenantId}/accounts/${accountId}/imports`,
    {
      method: "POST",
      json: { fileToken, currency },
    },
  );
}

export async function applyAccountImportPlan(
  tenantId: string,
  fileToken: string,
  groups: ImportPlanGroup[],
): Promise<ImportApplyResult> {
  return request<ImportApplyResult>(
    `/api/v1/t/${tenantId}/accounts/imports/apply-plan`,
    {
      method: "POST",
      json: { fileToken, groups },
    },
  );
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

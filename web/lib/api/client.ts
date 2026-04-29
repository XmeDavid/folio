// Typed Folio API client. Generated types live next to this file in schema.d.ts.
//
// Every request includes:
//   - credentials: "include" so the session cookie is sent
//   - X-Folio-Request: 1 to satisfy the backend CSRF header check
//
// Workspace-scoped resources live under /api/v1/t/{workspaceId}/…. Each helper takes
// an explicit workspaceId as the first argument so callers can never fall back to
// an ambient "current" workspace.

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
  init: RequestInit & { json?: unknown } = {}
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

export async function logout(): Promise<void> {
  return request<void>("/api/v1/auth/logout", { method: "POST" });
}

export type CreateWorkspaceInput = {
  name: string;
  baseCurrency: string;
  cycleAnchorDay: number;
  locale: string;
  timezone: string;
};

export type CreateWorkspaceResult = {
  workspace: {
    id: string;
    name: string;
    slug: string;
    baseCurrency: string;
    cycleAnchorDay: number;
    locale: string;
    timezone: string;
    createdAt: string;
  };
  membership: { workspaceId: string; userId: string; role: string; joinedAt: string };
};

export async function createWorkspace(input: CreateWorkspaceInput): Promise<CreateWorkspaceResult> {
  return request<CreateWorkspaceResult>("/api/v1/workspaces", {
    method: "POST",
    body: JSON.stringify(input),
  });
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

export async function confirmTOTP(
  code: string
): Promise<{ recoveryCodes: string[] }> {
  return request<{ recoveryCodes: string[] }>("/api/v1/me/mfa/totp/confirm", {
    method: "POST",
    json: { code },
  });
}

export async function beginPasskeyEnrollment(): Promise<{
  options: unknown;
  session: string;
}> {
  return request<{ options: unknown; session: string }>(
    "/api/v1/me/mfa/passkeys/begin",
    {
      method: "POST",
    }
  );
}

export async function completePasskeyEnrollment(
  session: string,
  credential: unknown,
  label = "Passkey"
): Promise<void> {
  return request<void>("/api/v1/me/mfa/passkeys/complete", {
    method: "POST",
    json: { session, label, credential },
  });
}

export async function regenerateRecoveryCodes(): Promise<{
  recoveryCodes: string[];
}> {
  return request<{ recoveryCodes: string[] }>("/api/v1/me/mfa/recovery-codes", {
    method: "POST",
  });
}

export async function disableTOTP(): Promise<void> {
  return request<void>("/api/v1/me/mfa/totp", { method: "DELETE" });
}

export type Passkey = {
  id: string;
  label: string;
  createdAt: string;
};

export async function listPasskeys(): Promise<Passkey[]> {
  return request<Passkey[]>("/api/v1/me/mfa/passkeys", { method: "GET" });
}

export async function deletePasskey(id: string): Promise<void> {
  return request<void>(`/api/v1/me/mfa/passkeys/${id}`, { method: "DELETE" });
}

export async function reauth(password: string, code?: string): Promise<void> {
  return request<void>("/api/v1/auth/reauth", {
    method: "POST",
    json: { password, code },
  });
}

export async function changePassword(input: {
  current: string;
  next: string;
}): Promise<void> {
  return request<void>("/api/v1/me/password", {
    method: "POST",
    json: input,
  });
}

export async function updateProfile(input: { displayName?: string }): Promise<void> {
  return request<void>("/api/v1/me", {
    method: "PATCH",
    json: input,
  });
}

/**
 * updateLastWorkspace records the user's most recently used workspace so the
 * next /login lands them back where they left off. Throws on failure; the
 * workspace switcher catches and swallows so navigation isn't blocked.
 */
export async function updateLastWorkspace(workspaceId: string): Promise<void> {
  return request<void>("/api/v1/me/last-workspace", {
    method: "PATCH",
    json: { workspaceId },
  });
}

// ---------------------------------------------------------------------------
// Workspace
// ---------------------------------------------------------------------------

export type WorkspacePatchInput = {
  name?: string;
  slug?: string;
  baseCurrency?: string;
  cycleAnchorDay?: number;
};

export type WorkspaceRow = {
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

export async function patchWorkspace(
  workspaceId: string,
  body: WorkspacePatchInput
): Promise<WorkspaceRow> {
  return request<WorkspaceRow>(`/api/v1/t/${workspaceId}`, {
    method: "PATCH",
    json: body,
  });
}

export async function deleteWorkspace(workspaceId: string): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}`, { method: "DELETE" });
}

export async function restoreWorkspace(workspaceId: string): Promise<WorkspaceRow> {
  return request<WorkspaceRow>(`/api/v1/t/${workspaceId}/restore`, {
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
  workspaceId: string;
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

export async function getMembers(workspaceId: string): Promise<MembersResponse> {
  return request<MembersResponse>(`/api/v1/t/${workspaceId}/members`, {
    method: "GET",
  });
}

export async function patchMember(
  workspaceId: string,
  userId: string,
  role: MemberRole
): Promise<MemberWithUser> {
  return request<MemberWithUser>(`/api/v1/t/${workspaceId}/members/${userId}`, {
    method: "PATCH",
    json: { role },
  });
}

export async function removeMember(
  workspaceId: string,
  userId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/members/${userId}`, {
    method: "DELETE",
  });
}

export type InviteCreateInput = {
  email: string;
  role: MemberRole;
};

export type WorkspaceInviteCreated = {
  invite: PendingInvite;
  acceptUrl: string;
};

export async function createInvite(
  workspaceId: string,
  body: InviteCreateInput
): Promise<WorkspaceInviteCreated> {
  return request<WorkspaceInviteCreated>(
    `/api/v1/t/${workspaceId}/invites`,
    { method: "POST", json: body }
  );
}

export async function revokeInvite(
  workspaceId: string,
  inviteId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/invites/${inviteId}`, {
    method: "DELETE",
  });
}

export async function resendInvite(
  workspaceId: string,
  inviteId: string
): Promise<WorkspaceInviteCreated> {
  return request<WorkspaceInviteCreated>(
    `/api/v1/t/${workspaceId}/invites/${inviteId}/resend`,
    { method: "POST" }
  );
}

export type InvitePreview = {
  workspaceId: string;
  workspaceName: string;
  workspaceSlug: string;
  inviterDisplayName: string;
  email: string;
  role: MemberRole;
  expiresAt: string;
};

export type InviteAcceptResponse = {
  workspaceId: string;
  userId: string;
  role: MemberRole;
  createdAt: string;
};

export async function previewInvite(token: string): Promise<InvitePreview> {
  return request<InvitePreview>(
    `/api/v1/auth/invites/${encodeURIComponent(token)}`,
    { method: "GET" }
  );
}

export type PlatformInvitePreview = {
  email: string | null;
  expiresAt: string;
};

export async function previewPlatformInvite(
  token: string
): Promise<PlatformInvitePreview> {
  return request<PlatformInvitePreview>(
    `/api/v1/auth/platform-invites/${encodeURIComponent(token)}`,
    { method: "GET" }
  );
}

export async function acceptInvite(
  token: string
): Promise<InviteAcceptResponse> {
  return request<InviteAcceptResponse>(
    `/api/v1/auth/invites/${encodeURIComponent(token)}/accept`,
    { method: "POST" }
  );
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

export async function fetchAccounts(
  workspaceId: string,
  opts: { includeArchived?: boolean } = {}
): Promise<Account[]> {
  const qs = opts.includeArchived ? "?includeArchived=true" : "";
  return request<Account[]>(`/api/v1/t/${workspaceId}/accounts${qs}`, {
    method: "GET",
  });
}

export async function createAccount(
  workspaceId: string,
  body: AccountCreateInput
): Promise<Account> {
  return request<Account>(`/api/v1/t/${workspaceId}/accounts`, {
    method: "POST",
    json: body,
  });
}

export type AccountPatchInput = {
  name?: string;
  nickname?: string | null;
  kind?: AccountKind;
  institution?: string | null;
  accountGroupId?: string | null;
  accountSortOrder?: number;
  includeInNetworth?: boolean;
  includeInSavingsRate?: boolean;
  closeDate?: string | null;
  archived?: boolean;
};

export async function updateAccount(
  workspaceId: string,
  accountId: string,
  body: AccountPatchInput
): Promise<Account> {
  return request<Account>(`/api/v1/t/${workspaceId}/accounts/${accountId}`, {
    method: "PATCH",
    json: body,
  });
}

export async function deleteAccount(
  workspaceId: string,
  accountId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/accounts/${accountId}`, {
    method: "DELETE",
  });
}

export async function fetchAccountGroups(
  workspaceId: string,
  opts: { includeArchived?: boolean } = {}
): Promise<AccountGroup[]> {
  return request<AccountGroup[]>(
    `/api/v1/t/${workspaceId}/accounts/groups${buildQuery({
      includeArchived: opts.includeArchived,
    })}`,
    { method: "GET" }
  );
}

export async function createAccountGroup(
  workspaceId: string,
  body: AccountGroupCreateInput
): Promise<AccountGroup> {
  return request<AccountGroup>(`/api/v1/t/${workspaceId}/accounts/groups`, {
    method: "POST",
    json: body,
  });
}

export async function updateAccountGroup(
  workspaceId: string,
  groupId: string,
  body: AccountGroupUpdateInput
): Promise<AccountGroup> {
  return request<AccountGroup>(
    `/api/v1/t/${workspaceId}/accounts/groups/${groupId}`,
    {
      method: "PATCH",
      json: body,
    }
  );
}

export async function deleteAccountGroup(
  workspaceId: string,
  groupId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/accounts/groups/${groupId}`, {
    method: "DELETE",
  });
}

export async function reorderAccounts(
  workspaceId: string,
  body: AccountReorderInput
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/accounts/order`, {
    method: "PUT",
    json: body,
  });
}

export type ImportPreviewRow = {
  bookedAt: string;
  amount: string;
  currency: string;
  description: string;
};

export type ImportConflictPreview = {
  reason?: "description_mismatch" | "date_drift";
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
  archived?: boolean;
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
  reactivate?: boolean;
};

export type ImportApplyResult = {
  batchId: string;
  insertedCount: number;
  duplicateCount: number;
  conflictCount: number;
  transactionIds: string[];
  conflicts?: ImportConflictPreview[];
};

// SmartInvestmentImportResult mirrors backend internal/investments
// SmartImportResult. Returned inline by the smart-import dispatcher when the
// uploaded file was an investment activity statement (IBKR / Revolut Trading).
export type SmartInvestmentImportResult = {
  detected: true;
  source: "ibkr" | "revolut_trading";
  accountId: string;
  accountName: string;
  baseCurrency: string;
  created: boolean;
  summary: {
    tradesCreated: number;
    dividendsCreated: number;
    instrumentsTouched: number;
    skipped: number;
    warnings?: string[];
  };
};

// SmartImportResponse is the discriminated union the smart-import dispatcher
// returns. UI branches on `kind` to either render the investment summary
// (already ingested, no apply step) or fall through to the bank-import
// preview/apply UX.
export type SmartImportResponse =
  | { kind: "investment"; investment: SmartInvestmentImportResult }
  | { kind: "bank"; preview: ImportPreview };

export async function previewAccountImport(
  workspaceId: string,
  file: File,
  accountId?: string
): Promise<SmartImportResponse> {
  const form = new FormData();
  form.append("file", file);
  if (accountId) {
    // Targeted variant — returns the bank Preview directly (no smart dispatch).
    const preview = await uploadRequest<ImportPreview>(
      `/api/v1/t/${workspaceId}/accounts/${accountId}/imports/preview`,
      form
    );
    return { kind: "bank", preview };
  }
  return uploadRequest<SmartImportResponse>(
    `/api/v1/t/${workspaceId}/accounts/import-preview`,
    form
  );
}

export async function applyAccountImport(
  workspaceId: string,
  accountId: string,
  fileToken: string,
  currency?: string
): Promise<ImportApplyResult> {
  return request<ImportApplyResult>(
    `/api/v1/t/${workspaceId}/accounts/${accountId}/imports`,
    {
      method: "POST",
      json: { fileToken, currency },
    }
  );
}

export async function applyAccountImportPlan(
  workspaceId: string,
  fileToken: string,
  groups: ImportPlanGroup[]
): Promise<ImportApplyResult> {
  return request<ImportApplyResult>(
    `/api/v1/t/${workspaceId}/accounts/imports/apply-plan`,
    {
      method: "POST",
      json: { fileToken, groups },
    }
  );
}

// MultiSmartImportEntry is one element of the multi-file preview response.
// `kind` discriminates: bank → preview is the full ImportPreview; investment
// → already-ingested summary; error → file couldn't be read or parsed and
// the user should be told why without aborting the rest of the batch.
export type MultiSmartImportEntry =
  | { kind: "bank"; fileName: string; preview: ImportPreview }
  | {
      kind: "investment";
      fileName: string;
      investment: SmartInvestmentImportResult;
    }
  | { kind: "error"; fileName: string; error: string };

export type MultiSmartImportResponse = {
  files: MultiSmartImportEntry[];
};

export async function previewAccountImportMulti(
  workspaceId: string,
  files: File[]
): Promise<MultiSmartImportResponse> {
  const form = new FormData();
  for (const file of files) {
    form.append("files", file);
  }
  return uploadRequest<MultiSmartImportResponse>(
    `/api/v1/t/${workspaceId}/accounts/imports/multi-preview`,
    form
  );
}

export type MultiApplyFileInput = {
  fileToken: string;
  groups: ImportPlanGroup[];
};

export type MultiApplyFileResult = {
  fileName?: string;
  result?: ImportApplyResult;
  error?: string;
};

export type MultiApplyResult = {
  files: MultiApplyFileResult[];
  insertedCount: number;
  duplicateCount: number;
  conflictCount: number;
};

export async function applyAccountImportMulti(
  workspaceId: string,
  files: MultiApplyFileInput[]
): Promise<MultiApplyResult> {
  return request<MultiApplyResult>(
    `/api/v1/t/${workspaceId}/accounts/imports/apply-multi`,
    {
      method: "POST",
      json: { files },
    }
  );
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

type TransactionsQuery = {
  accountId?: string;
  categoryId?: string;
  merchantId?: string;
  from?: string;
  to?: string;
  status?: TransactionStatus;
  search?: string;
  minAmount?: string;
  maxAmount?: string;
  uncategorized?: boolean;
  excludeInvestments?: boolean;
  limit?: number;
  offset?: number;
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
  workspaceId: string,
  query: TransactionsQuery = {}
): Promise<Transaction[]> {
  return request<Transaction[]>(
    `/api/v1/t/${workspaceId}/transactions${buildQuery(query)}`,
    { method: "GET" }
  );
}

export async function createTransaction(
  workspaceId: string,
  body: TransactionCreateInput
): Promise<Transaction> {
  return request<Transaction>(`/api/v1/t/${workspaceId}/transactions`, {
    method: "POST",
    json: body,
  });
}

export type TransactionUpdateInput =
  components["schemas"]["TransactionUpdateInput"];

export async function fetchTransaction(
  workspaceId: string,
  transactionId: string
): Promise<Transaction> {
  return request<Transaction>(
    `/api/v1/t/${workspaceId}/transactions/${transactionId}`,
    { method: "GET" }
  );
}

export async function updateTransaction(
  workspaceId: string,
  transactionId: string,
  body: TransactionUpdateInput
): Promise<Transaction> {
  return request<Transaction>(
    `/api/v1/t/${workspaceId}/transactions/${transactionId}`,
    {
      method: "PATCH",
      json: body,
    }
  );
}

export async function deleteTransaction(
  workspaceId: string,
  transactionId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/transactions/${transactionId}`, {
    method: "DELETE",
  });
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

export type Category = {
  id: string;
  workspaceId: string;
  parentId?: string | null;
  name: string;
  color?: string | null;
  sortOrder: number;
  archivedAt?: string | null;
  createdAt: string;
  updatedAt: string;
};

export type CategoryCreateInput = {
  parentId?: string | null;
  name: string;
  color?: string | null;
  sortOrder?: number;
};

export type CategoryPatchInput = {
  parentId?: string | null;
  name?: string;
  color?: string | null;
  sortOrder?: number;
  archived?: boolean;
};

export type Merchant = {
  id: string;
  workspaceId: string;
  canonicalName: string;
  logoUrl?: string | null;
  defaultCategoryId?: string | null;
  industry?: string | null;
  website?: string | null;
  notes?: string | null;
  archivedAt?: string | null;
  createdAt: string;
  updatedAt: string;
};

export type MerchantCreateInput = {
  canonicalName: string;
  defaultCategoryId?: string | null;
  industry?: string | null;
  website?: string | null;
  notes?: string | null;
  logoUrl?: string | null;
};

export type MerchantPatchInput = {
  canonicalName?: string | null;
  defaultCategoryId?: string | null;
  industry?: string | null;
  website?: string | null;
  notes?: string | null;
  logoUrl?: string | null;
  archived?: boolean;
  /** When `defaultCategoryId` is changing AND cascade is true, the server
   *  also re-categorises every transaction whose merchant_id matches this
   *  merchant and whose category_id equals the merchant's PREVIOUS default. */
  cascade?: boolean;
};

export type MerchantPatchResult = {
  merchant: Merchant;
  cascadedTransactionCount?: number;
};

export type MerchantAlias = {
  id: string;
  workspaceId: string;
  merchantId: string;
  rawPattern: string;
  createdAt: string;
};

export type MergePreview = {
  sourceCanonicalName: string;
  targetCanonicalName: string;
  movedCount: number;
  capturedAliasCount: number;
  cascadedCountIfApplied: number;
  blankFillFields: string[];
};

export type MergeResult = {
  target: Merchant;
  movedCount: number;
  cascadedCount: number;
  capturedAliasCount: number;
};

export async function fetchCategories(
  workspaceId: string,
  opts: { includeArchived?: boolean } = {}
): Promise<Category[]> {
  return request<Category[]>(
    `/api/v1/t/${workspaceId}/categories${buildQuery({
      includeArchived: opts.includeArchived,
    })}`,
    { method: "GET" }
  );
}

export async function createCategory(
  workspaceId: string,
  body: CategoryCreateInput
): Promise<Category> {
  return request<Category>(`/api/v1/t/${workspaceId}/categories`, {
    method: "POST",
    json: body,
  });
}

export async function updateCategory(
  workspaceId: string,
  categoryId: string,
  body: CategoryPatchInput
): Promise<Category> {
  return request<Category>(`/api/v1/t/${workspaceId}/categories/${categoryId}`, {
    method: "PATCH",
    json: body,
  });
}

export async function archiveCategory(
  workspaceId: string,
  categoryId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/categories/${categoryId}`, {
    method: "DELETE",
  });
}

export async function fetchMerchants(
  workspaceId: string,
  opts: { includeArchived?: boolean } = {}
): Promise<Merchant[]> {
  return request<Merchant[]>(
    `/api/v1/t/${workspaceId}/merchants${buildQuery({
      includeArchived: opts.includeArchived,
    })}`,
    { method: "GET" }
  );
}

export async function fetchMerchant(
  workspaceId: string,
  id: string
): Promise<Merchant> {
  return request<Merchant>(`/api/v1/t/${workspaceId}/merchants/${id}`, {
    method: "GET",
  });
}

export async function createMerchant(
  workspaceId: string,
  body: MerchantCreateInput
): Promise<Merchant> {
  return request<Merchant>(`/api/v1/t/${workspaceId}/merchants`, {
    method: "POST",
    json: body,
  });
}

export async function updateMerchant(
  workspaceId: string,
  merchantId: string,
  body: MerchantPatchInput
): Promise<MerchantPatchResult> {
  return request<MerchantPatchResult>(
    `/api/v1/t/${workspaceId}/merchants/${merchantId}`,
    { method: "PATCH", json: body }
  );
}

export async function archiveMerchant(
  workspaceId: string,
  merchantId: string
): Promise<void> {
  return request<void>(`/api/v1/t/${workspaceId}/merchants/${merchantId}`, {
    method: "DELETE",
  });
}

export async function listMerchantAliases(
  workspaceId: string,
  merchantId: string
): Promise<MerchantAlias[]> {
  return request<MerchantAlias[]>(
    `/api/v1/t/${workspaceId}/merchants/${merchantId}/aliases`,
    { method: "GET" }
  );
}

export async function addMerchantAlias(
  workspaceId: string,
  merchantId: string,
  body: { rawPattern: string }
): Promise<MerchantAlias> {
  return request<MerchantAlias>(
    `/api/v1/t/${workspaceId}/merchants/${merchantId}/aliases`,
    { method: "POST", json: body }
  );
}

export async function removeMerchantAlias(
  workspaceId: string,
  merchantId: string,
  aliasId: string
): Promise<void> {
  return request<void>(
    `/api/v1/t/${workspaceId}/merchants/${merchantId}/aliases/${aliasId}`,
    { method: "DELETE" }
  );
}

export async function previewMergeMerchants(
  workspaceId: string,
  sourceMerchantId: string,
  body: { targetId: string }
): Promise<MergePreview> {
  return request<MergePreview>(
    `/api/v1/t/${workspaceId}/merchants/${sourceMerchantId}/merge/preview`,
    { method: "POST", json: body }
  );
}

export async function mergeMerchants(
  workspaceId: string,
  sourceMerchantId: string,
  body: { targetId: string; applyTargetDefault: boolean }
): Promise<MergeResult> {
  return request<MergeResult>(
    `/api/v1/t/${workspaceId}/merchants/${sourceMerchantId}/merge`,
    { method: "POST", json: body }
  );
}

// ---------------------------------------------------------------------------
// Types re-exported from the OpenAPI-generated schema. Schema regeneration is
// not part of this change; types that reference removed endpoints still live
// in schema.d.ts until a regeneration pass syncs with the v2 spec.
// ---------------------------------------------------------------------------

export type Account = components["schemas"]["Account"];
export type AccountKind = components["schemas"]["AccountKind"];
export type AccountCreateInput = components["schemas"]["AccountCreateInput"];
export type AccountGroup = components["schemas"]["AccountGroup"];
export type AccountGroupCreateInput =
  components["schemas"]["AccountGroupCreateInput"];
export type AccountGroupUpdateInput =
  components["schemas"]["AccountGroupUpdateInput"];
export type AccountReorderInput = components["schemas"]["AccountReorderInput"];
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

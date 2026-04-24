// Temporary tenant-id bridge backed by localStorage. This is a dev-only stand-in
// for real session auth: the backend currently accepts any X-Tenant-ID header,
// so the client stores the id returned from /api/v1/onboarding/bootstrap and
// echoes it back on every tenant-scoped request.
//
// Treat this module as the single chokepoint for that bridge - when session
// cookies replace the header, only this file changes.

const TENANT_KEY = "folio.tenantId";
const USER_KEY = "folio.userId";

export function readTenantId(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(TENANT_KEY);
}

export function readUserId(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(USER_KEY);
}

export function saveIdentity(tenantId: string, userId: string) {
  window.localStorage.setItem(TENANT_KEY, tenantId);
  window.localStorage.setItem(USER_KEY, userId);
  window.dispatchEvent(new CustomEvent("folio:identity"));
}

export function clearIdentity() {
  window.localStorage.removeItem(TENANT_KEY);
  window.localStorage.removeItem(USER_KEY);
  window.dispatchEvent(new CustomEvent("folio:identity"));
}

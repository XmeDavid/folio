"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...init,
    credentials: "include",
    headers: { "X-Folio-Request": "1", ...(init.headers ?? {}) },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw Object.assign(new Error(body.error ?? `request failed: ${res.status}`), {
      status: res.status,
      code: body.code,
    });
  }
  return (await res.json()) as T;
}

export type Pagination = { limit: number; nextCursor?: string };
export type Envelope<T> = { data: T; pagination?: Pagination };
export type Workspace = {
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
export type User = {
  id: string;
  email: string;
  displayName: string;
  emailVerifiedAt?: string;
  isAdmin: boolean;
  createdAt: string;
};
export type WorkspaceDetail = {
  workspace: Workspace;
  memberCount: number;
  deletedAt?: string;
  lastActivityAt?: string;
};
export type UserDetail = {
  user: User;
  memberships: { workspaceId: string; workspaceName: string; workspaceSlug: string; role: string; joinedAt: string }[];
  activeSessions: { id: string; createdAt: string; lastSeenAt: string; userAgent: string; ip?: string }[];
  mfa: { passkeys: { id: string; label: string; createdAt: string }[]; totpEnabled: boolean; recoveryCodesRemaining: number };
  lastLoginAt?: string;
};
export type AuditEvent = {
  id: string;
  workspaceId?: string;
  actorUserId?: string;
  entityType: string;
  entityId: string;
  action: string;
  occurredAt: string;
};
export type Job = {
  id: number;
  kind: string;
  queue: string;
  state: string;
  scheduledAt: string;
  attemptedAt?: string;
};

const qs = (params: Record<string, string | number | boolean | undefined>) => {
  const s = new URLSearchParams();
  Object.entries(params).forEach(([k, v]) => {
    if (v !== undefined && v !== "") s.set(k, String(v));
  });
  const out = s.toString();
  return out ? `?${out}` : "";
};

export function useAdminWorkspaces(params: { search?: string; includeDeleted?: boolean; cursor?: string }) {
  return useQuery({ queryKey: ["admin", "workspaces", params], queryFn: () => api<Envelope<Workspace[]>>(`/api/v1/admin/workspaces${qs(params)}`) });
}

export function useAdminWorkspaceDetail(workspaceId: string) {
  return useQuery({ queryKey: ["admin", "workspace", workspaceId], queryFn: () => api<Envelope<WorkspaceDetail>>(`/api/v1/admin/workspaces/${workspaceId}`), enabled: !!workspaceId });
}

export function useAdminUsers(params: { search?: string; isAdminOnly?: boolean; cursor?: string }) {
  return useQuery({ queryKey: ["admin", "users", params], queryFn: () => api<Envelope<User[]>>(`/api/v1/admin/users${qs(params)}`) });
}

export function useAdminUserDetail(userId: string) {
  return useQuery({ queryKey: ["admin", "user", userId], queryFn: () => api<Envelope<UserDetail>>(`/api/v1/admin/users/${userId}`), enabled: !!userId });
}

export function useAdminAudit(params: { action?: string; cursor?: string }) {
  return useQuery({ queryKey: ["admin", "audit", params], queryFn: () => api<Envelope<AuditEvent[]>>(`/api/v1/admin/audit${qs(params)}`) });
}

export function useAdminJobs(params: { state?: string; kind?: string; cursor?: string }) {
  return useQuery({ queryKey: ["admin", "jobs", params], queryFn: () => api<Envelope<Job[]>>(`/api/v1/admin/jobs${qs(params)}`) });
}

export function useAdminMutation(pathFor: (id: string) => string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<{ ok: true }>(pathFor(id), { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin"] }),
  });
}

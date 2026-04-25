"use client";

import { useParams } from "next/navigation";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAdminMutation, useAdminUserDetail } from "@/lib/admin/client";

export default function AdminUserDetailPage() {
  const { userId } = useParams<{ userId: string }>();
  const q = useAdminUserDetail(userId);
  const grant = useAdminMutation((id) => `/api/v1/admin/users/${id}/grant-admin`);
  const revoke = useAdminMutation((id) => `/api/v1/admin/users/${id}/revoke-admin`);
  const d = q.data?.data;
  if (!d) return <p className="text-sm text-fg-muted">Loading...</p>;
  return (
    <section className="space-y-6">
      <div className="flex flex-col gap-3 border-b border-border pb-5 sm:flex-row sm:items-start sm:justify-between">
        <div><div className="flex items-center gap-2"><h1 className="text-[28px] font-normal">{d.user.email}</h1>{d.user.isAdmin ? <Badge variant="danger">Admin</Badge> : null}</div><p className="text-[14px] text-fg-muted">{d.user.displayName}</p></div>
        {d.user.isAdmin ? <Button variant="danger" onClick={() => revoke.mutate(d.user.id)}>Revoke admin</Button> : <Button onClick={() => grant.mutate(d.user.id)}>Grant admin</Button>}
      </div>
      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="Memberships">{d.memberships.map((m) => <Row key={`${m.tenantId}-${m.role}`} left={m.tenantName} right={m.role} />)}</Panel>
        <Panel title="MFA"><Row left="TOTP" right={d.mfa.totpEnabled ? "Enabled" : "Off"} /><Row left="Passkeys" right={String(d.mfa.passkeys.length)} /><Row left="Recovery codes" right={String(d.mfa.recoveryCodesRemaining)} /></Panel>
        <Panel title="Sessions">{d.activeSessions.map((s) => <Row key={s.id} left={s.userAgent || s.id} right={new Date(s.lastSeenAt).toLocaleString()} />)}</Panel>
      </div>
    </section>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return <section className="border border-border bg-surface p-4"><h2 className="mb-3 text-[15px] font-medium">{title}</h2><div className="space-y-2">{children}</div></section>;
}

function Row({ left, right }: { left: string; right: string }) {
  return <div className="flex justify-between gap-4 border-t border-border pt-2 text-[14px]"><span className="text-fg-muted">{left}</span><span className="text-right">{right}</span></div>;
}

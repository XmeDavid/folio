"use client";

import { useParams } from "next/navigation";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { InfoPanel, MetaRow } from "@/components/app/info-panel";
import { LoadingText } from "@/components/app/empty";
import { useAdminMutation, useAdminUserDetail } from "@/lib/admin/client";
import { formatDateTime } from "@/lib/format";

export default function AdminUserDetailPage() {
  const { userId } = useParams<{ userId: string }>();
  const q = useAdminUserDetail(userId);
  const grant = useAdminMutation(
    (id) => `/api/v1/admin/users/${id}/grant-admin`
  );
  const revoke = useAdminMutation(
    (id) => `/api/v1/admin/users/${id}/revoke-admin`
  );
  const d = q.data?.data;
  if (!d) return <LoadingText />;
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-3 border-b border-border pb-5 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-[28px] font-normal tracking-tight">
              {d.user.email}
            </h1>
            {d.user.isAdmin ? <Badge variant="danger">Admin</Badge> : null}
          </div>
          <p className="text-[14px] text-fg-muted">{d.user.displayName}</p>
        </div>
        {d.user.isAdmin ? (
          <Button variant="danger" onClick={() => revoke.mutate(d.user.id)}>
            Revoke admin
          </Button>
        ) : (
          <Button onClick={() => grant.mutate(d.user.id)}>Grant admin</Button>
        )}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <InfoPanel title="Memberships">
          {d.memberships.map((m) => (
            <MetaRow
              key={`${m.tenantId}-${m.role}`}
              label={m.tenantName}
              value={m.role}
            />
          ))}
        </InfoPanel>

        <InfoPanel title="MFA">
          <MetaRow label="TOTP" value={d.mfa.totpEnabled ? "Enabled" : "Off"} />
          <MetaRow label="Passkeys" value={String(d.mfa.passkeys.length)} />
          <MetaRow
            label="Recovery codes"
            value={String(d.mfa.recoveryCodesRemaining)}
          />
        </InfoPanel>

        <InfoPanel title="Sessions">
          {d.activeSessions.map((s) => (
            <MetaRow
              key={s.id}
              label={s.userAgent || s.id}
              value={formatDateTime(s.lastSeenAt)}
            />
          ))}
        </InfoPanel>
      </div>
    </section>
  );
}

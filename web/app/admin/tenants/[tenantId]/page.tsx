"use client";

import { useParams } from "next/navigation";
import { Badge } from "@/components/ui/badge";
import { useAdminTenantDetail } from "@/lib/admin/client";

export default function AdminTenantDetailPage() {
  const { tenantId } = useParams<{ tenantId: string }>();
  const q = useAdminTenantDetail(tenantId);
  const d = q.data?.data;
  if (!d) return <p className="text-sm text-[--color-fg-muted]">Loading...</p>;
  return (
    <section className="space-y-5">
      <div className="border-b border-[--color-border] pb-5">
        <div className="flex items-center gap-2">
          <h1 className="text-[28px] font-normal">{d.tenant.name}</h1>
          {d.deletedAt ? <Badge variant="amber">Deleted</Badge> : <Badge variant="success">Active</Badge>}
        </div>
        <p className="text-[14px] text-[--color-fg-muted]">{d.tenant.slug}</p>
      </div>
      <dl className="grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2">
        <Meta label="Tenant ID" value={d.tenant.id} />
        <Meta label="Members" value={String(d.memberCount)} />
        <Meta label="Base currency" value={d.tenant.baseCurrency} />
        <Meta label="Cycle anchor" value={String(d.tenant.cycleAnchorDay)} />
        <Meta label="Locale" value={d.tenant.locale} />
        <Meta label="Timezone" value={d.tenant.timezone} />
        <Meta label="Created" value={formatDateTime(d.tenant.createdAt)} />
        <Meta label="Last activity" value={formatDateTime(d.lastActivityAt)} />
      </dl>
    </section>
  );
}

function Meta({ label, value }: { label: string; value?: string }) {
  return <div className="border-b border-[--color-border] pb-3"><dt className="text-[12px] text-[--color-fg-faint]">{label}</dt><dd className="mt-1 break-all text-[14px]">{value || "None"}</dd></div>;
}

function formatDateTime(v?: string) {
  return v ? new Date(v).toLocaleString() : undefined;
}

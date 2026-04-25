"use client";

import { useParams } from "next/navigation";
import { Badge } from "@/components/ui/badge";
import { MetaItem } from "@/components/app/info-panel";
import { LoadingText } from "@/components/app/empty";
import { useAdminTenantDetail } from "@/lib/admin/client";
import { formatDateTime } from "@/lib/format";

export default function AdminTenantDetailPage() {
  const { tenantId } = useParams<{ tenantId: string }>();
  const q = useAdminTenantDetail(tenantId);
  const d = q.data?.data;
  if (!d) return <LoadingText />;
  return (
    <section className="flex flex-col gap-5">
      <div className="border-b border-border pb-5">
        <div className="flex items-center gap-2">
          <h1 className="text-[28px] font-normal tracking-tight">
            {d.tenant.name}
          </h1>
          {d.deletedAt ? (
            <Badge variant="amber">Deleted</Badge>
          ) : (
            <Badge variant="success">Active</Badge>
          )}
        </div>
        <p className="text-[14px] text-fg-muted">{d.tenant.slug}</p>
      </div>
      <dl className="grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2">
        <MetaItem label="Tenant ID" value={d.tenant.id} />
        <MetaItem label="Members" value={String(d.memberCount)} />
        <MetaItem label="Base currency" value={d.tenant.baseCurrency} />
        <MetaItem
          label="Cycle anchor"
          value={String(d.tenant.cycleAnchorDay)}
        />
        <MetaItem label="Locale" value={d.tenant.locale} />
        <MetaItem label="Timezone" value={d.tenant.timezone} />
        <MetaItem label="Created" value={formatDateTime(d.tenant.createdAt)} />
        <MetaItem
          label="Last activity"
          value={formatDateTime(d.lastActivityAt)}
        />
      </dl>
    </section>
  );
}

"use client";

import { useParams } from "next/navigation";
import { Badge } from "@/components/ui/badge";
import { MetaItem } from "@/components/app/info-panel";
import { LoadingText } from "@/components/app/empty";
import { useAdminWorkspaceDetail } from "@/lib/admin/client";
import { formatDateTime } from "@/lib/format";

export default function AdminWorkspaceDetailPage() {
  const { workspaceId } = useParams<{ workspaceId: string }>();
  const q = useAdminWorkspaceDetail(workspaceId);
  const d = q.data?.data;
  if (!d) return <LoadingText />;
  return (
    <section className="flex flex-col gap-5">
      <div className="border-b border-border pb-5">
        <div className="flex items-center gap-2">
          <h1 className="text-[28px] font-normal tracking-tight">
            {d.workspace.name}
          </h1>
          {d.deletedAt ? (
            <Badge variant="amber">Deleted</Badge>
          ) : (
            <Badge variant="success">Active</Badge>
          )}
        </div>
        <p className="text-[14px] text-fg-muted">{d.workspace.slug}</p>
      </div>
      <dl className="grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2">
        <MetaItem label="Workspace ID" value={d.workspace.id} />
        <MetaItem label="Members" value={String(d.memberCount)} />
        <MetaItem label="Base currency" value={d.workspace.baseCurrency} />
        <MetaItem
          label="Cycle anchor"
          value={String(d.workspace.cycleAnchorDay)}
        />
        <MetaItem label="Locale" value={d.workspace.locale} />
        <MetaItem label="Timezone" value={d.workspace.timezone} />
        <MetaItem label="Created" value={formatDateTime(d.workspace.createdAt)} />
        <MetaItem
          label="Last activity"
          value={formatDateTime(d.lastActivityAt)}
        />
      </dl>
    </section>
  );
}

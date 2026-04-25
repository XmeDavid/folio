"use client";

import { useState } from "react";
import { Input } from "@/components/ui/input";
import { DataTable } from "@/components/ui/data-table";
import { PageHeader } from "@/components/app/page-header";
import { useAdminAudit } from "@/lib/admin/client";
import { formatDateTime } from "@/lib/format";

type AdminAuditEvent = NonNullable<
  ReturnType<typeof useAdminAudit>["data"]
>["data"][number];

export default function AdminAuditPage() {
  const [action, setAction] = useState("");
  const q = useAdminAudit({ action });
  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Audit"
        description="Cross-tenant operational event feed."
      />
      <Input
        value={action}
        onChange={(e) => setAction(e.target.value)}
        placeholder="Filter by action prefix"
        className="max-w-md"
      />
      <DataTable<AdminAuditEvent>
        columns={[
          {
            header: "Action",
            cell: (e) => <span className="font-medium">{e.action}</span>,
          },
          { header: "Entity", cell: (e) => e.entityType },
          {
            header: "Actor",
            cell: (e) => (
              <span className="break-all">{e.actorUserId ?? "System"}</span>
            ),
          },
          { header: "Occurred", cell: (e) => formatDateTime(e.occurredAt) },
        ]}
        rows={q.data?.data ?? []}
        rowKey={(e) => e.id}
        isLoading={q.isLoading}
      />
    </section>
  );
}

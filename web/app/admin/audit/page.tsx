"use client";

import { useState } from "react";
import { Input } from "@/components/ui/input";
import { useAdminAudit } from "@/lib/admin/client";

export default function AdminAuditPage() {
  const [action, setAction] = useState("");
  const q = useAdminAudit({ action });
  return (
    <section className="space-y-5">
      <div className="border-b border-border pb-5"><h1 className="text-[28px] font-normal">Audit</h1><p className="text-[14px] text-fg-muted">Cross-tenant operational event feed.</p></div>
      <Input value={action} onChange={(e) => setAction(e.target.value)} placeholder="Filter by action prefix" className="max-w-md" />
      <div className="overflow-hidden border border-border bg-surface">
        <table className="w-full text-left text-[14px]">
          <thead className="bg-surface-subtle text-[12px] text-fg-muted uppercase"><tr><th className="px-4 py-3">Action</th><th>Entity</th><th>Actor</th><th>Occurred</th></tr></thead>
          <tbody>{(q.data?.data ?? []).map((e) => <tr key={e.id} className="border-t border-border"><td className="px-4 py-3 font-medium">{e.action}</td><td>{e.entityType}</td><td className="break-all">{e.actorUserId ?? "System"}</td><td>{new Date(e.occurredAt).toLocaleString()}</td></tr>)}</tbody>
        </table>
      </div>
    </section>
  );
}

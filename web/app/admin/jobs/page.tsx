"use client";

import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAdminJobs, useAdminMutation } from "@/lib/admin/client";

export default function AdminJobsPage() {
  const [state, setState] = useState("");
  const [kind, setKind] = useState("");
  const q = useAdminJobs({ state, kind });
  const retry = useAdminMutation((id) => `/api/v1/admin/jobs/${id}/retry`);
  return (
    <section className="space-y-5">
      <div className="border-b border-border pb-5"><h1 className="text-[28px] font-normal">Jobs</h1><p className="text-[14px] text-fg-muted">River queue inspection and retry.</p></div>
      <div className="flex max-w-2xl gap-3"><Input value={state} onChange={(e) => setState(e.target.value)} placeholder="State" /><Input value={kind} onChange={(e) => setKind(e.target.value)} placeholder="Kind" /></div>
      <div className="overflow-hidden border border-border bg-surface">
        <table className="w-full text-left text-[14px]">
          <thead className="bg-surface-subtle text-[12px] text-fg-muted uppercase"><tr><th className="px-4 py-3">Kind</th><th>Queue</th><th>State</th><th>Scheduled</th><th></th></tr></thead>
          <tbody>{(q.data?.data ?? []).map((j) => <tr key={j.id} className="border-t border-border"><td className="px-4 py-3 font-medium">{j.kind}</td><td>{j.queue}</td><td><Badge variant={j.state === "completed" ? "success" : j.state === "discarded" ? "danger" : "neutral"}>{j.state}</Badge></td><td>{new Date(j.scheduledAt).toLocaleString()}</td><td className="pr-4 text-right"><Button variant="secondary" size="sm" onClick={() => retry.mutate(String(j.id))}>Retry</Button></td></tr>)}</tbody>
        </table>
      </div>
    </section>
  );
}

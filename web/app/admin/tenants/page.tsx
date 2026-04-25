"use client";

import Link from "next/link";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAdminTenants } from "@/lib/admin/client";

export default function AdminTenantsPage() {
  const [search, setSearch] = useState("");
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const q = useAdminTenants({ search, includeDeleted });
  return (
    <section className="space-y-5">
      <div className="flex flex-col gap-3 border-b border-border pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-[28px] font-normal">Tenants</h1>
          <p className="text-[14px] text-fg-muted">Operational tenant metadata only.</p>
        </div>
        <label className="flex items-center gap-2 text-[13px] text-fg-muted">
          <input type="checkbox" checked={includeDeleted} onChange={(e) => setIncludeDeleted(e.target.checked)} />
          Include deleted
        </label>
      </div>
      <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search name, slug, or ID" className="max-w-md" />
      <div className="overflow-hidden border border-border bg-surface">
        <table className="w-full text-left text-[14px]">
          <thead className="bg-surface-subtle text-[12px] text-fg-muted uppercase">
            <tr><th className="px-4 py-3">Name</th><th>Slug</th><th>Currency</th><th>Created</th><th>Status</th></tr>
          </thead>
          <tbody>
            {(q.data?.data ?? []).map((t) => (
              <tr key={t.id} className="border-t border-border hover:bg-surface-subtle">
                <td className="px-4 py-3 font-medium"><Link href={`/admin/tenants/${t.id}`}>{t.name}</Link></td>
                <td>{t.slug}</td>
                <td>{t.baseCurrency}</td>
                <td>{formatDate(t.createdAt)}</td>
                <td>{t.deletedAt ? <Badge variant="amber">Deleted</Badge> : <Badge variant="success">Active</Badge>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {q.isLoading ? <p className="text-sm text-fg-muted">Loading...</p> : null}
      {q.data?.pagination?.nextCursor ? <Button variant="secondary" disabled>Next page</Button> : null}
    </section>
  );
}

function formatDate(v?: string) {
  return v ? new Date(v).toLocaleDateString() : "";
}

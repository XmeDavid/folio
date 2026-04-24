"use client";

import Link from "next/link";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { useAdminUsers } from "@/lib/admin/client";

export default function AdminUsersPage() {
  const [search, setSearch] = useState("");
  const [isAdminOnly, setIsAdminOnly] = useState(false);
  const q = useAdminUsers({ search, isAdminOnly });
  return (
    <section className="space-y-5">
      <div className="flex flex-col gap-3 border-b border-[--color-border] pb-5 sm:flex-row sm:items-end sm:justify-between">
        <div><h1 className="text-[28px] font-normal">Users</h1><p className="text-[14px] text-[--color-fg-muted]">Account, session, and admin status.</p></div>
        <label className="flex items-center gap-2 text-[13px] text-[--color-fg-muted]"><input type="checkbox" checked={isAdminOnly} onChange={(e) => setIsAdminOnly(e.target.checked)} /> Admins only</label>
      </div>
      <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search email, name, or ID" className="max-w-md" />
      <div className="overflow-hidden border border-[--color-border] bg-[--color-surface]">
        <table className="w-full text-left text-[14px]">
          <thead className="bg-[--color-surface-subtle] text-[12px] text-[--color-fg-muted] uppercase"><tr><th className="px-4 py-3">Email</th><th>Name</th><th>Admin</th><th>Created</th></tr></thead>
          <tbody>{(q.data?.data ?? []).map((u) => <tr key={u.id} className="border-t border-[--color-border] hover:bg-[--color-surface-subtle]"><td className="px-4 py-3 font-medium"><Link href={`/admin/users/${u.id}`}>{u.email}</Link></td><td>{u.displayName}</td><td>{u.isAdmin ? <Badge variant="danger">Admin</Badge> : <Badge>Standard</Badge>}</td><td>{new Date(u.createdAt).toLocaleDateString()}</td></tr>)}</tbody>
        </table>
      </div>
    </section>
  );
}

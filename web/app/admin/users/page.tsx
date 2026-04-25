"use client";

import Link from "next/link";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { DataTable } from "@/components/ui/data-table";
import { PageHeader } from "@/components/app/page-header";
import { useAdminUsers } from "@/lib/admin/client";
import { formatDate } from "@/lib/format";

type AdminUser = NonNullable<
  ReturnType<typeof useAdminUsers>["data"]
>["data"][number];

export default function AdminUsersPage() {
  const [search, setSearch] = useState("");
  const [isAdminOnly, setIsAdminOnly] = useState(false);
  const q = useAdminUsers({ search, isAdminOnly });
  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Users"
        description="Account, session, and admin status."
        actions={
          <label className="flex items-center gap-2 text-[13px] text-fg-muted">
            <input
              type="checkbox"
              checked={isAdminOnly}
              onChange={(e) => setIsAdminOnly(e.target.checked)}
            />
            Admins only
          </label>
        }
      />
      <Input
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder="Search email, name, or ID"
        className="max-w-md"
      />
      <DataTable<AdminUser>
        columns={[
          {
            header: "Email",
            cell: (u) => (
              <Link href={`/admin/users/${u.id}`} className="font-medium">
                {u.email}
              </Link>
            ),
          },
          { header: "Name", cell: (u) => u.displayName },
          {
            header: "Admin",
            cell: (u) =>
              u.isAdmin ? (
                <Badge variant="danger">Admin</Badge>
              ) : (
                <Badge>Standard</Badge>
              ),
          },
          { header: "Created", cell: (u) => formatDate(u.createdAt) },
        ]}
        rows={q.data?.data ?? []}
        rowKey={(u) => u.id}
        isLoading={q.isLoading}
      />
    </section>
  );
}

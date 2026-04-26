"use client";

import Link from "next/link";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataTable } from "@/components/ui/data-table";
import { PageHeader } from "@/components/app/page-header";
import { useAdminWorkspaces } from "@/lib/admin/client";
import { formatDate } from "@/lib/format";

type AdminWorkspace = NonNullable<
  ReturnType<typeof useAdminWorkspaces>["data"]
>["data"][number];

export default function AdminWorkspacesPage() {
  const [search, setSearch] = useState("");
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const q = useAdminWorkspaces({ search, includeDeleted });
  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Workspaces"
        description="Operational workspace metadata only."
        actions={
          <label className="flex items-center gap-2 text-[13px] text-fg-muted">
            <input
              type="checkbox"
              checked={includeDeleted}
              onChange={(e) => setIncludeDeleted(e.target.checked)}
            />
            Include deleted
          </label>
        }
      />
      <Input
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder="Search name, slug, or ID"
        className="max-w-md"
      />
      <DataTable<AdminWorkspace>
        columns={[
          {
            header: "Name",
            cell: (t) => (
              <Link href={`/admin/workspaces/${t.id}`} className="font-medium">
                {t.name}
              </Link>
            ),
          },
          { header: "Slug", cell: (t) => t.slug },
          { header: "Currency", cell: (t) => t.baseCurrency },
          { header: "Created", cell: (t) => formatDate(t.createdAt) },
          {
            header: "Status",
            cell: (t) =>
              t.deletedAt ? (
                <Badge variant="amber">Deleted</Badge>
              ) : (
                <Badge variant="success">Active</Badge>
              ),
          },
        ]}
        rows={q.data?.data ?? []}
        rowKey={(t) => t.id}
        isLoading={q.isLoading}
      />
      {q.data?.pagination?.nextCursor ? (
        <Button variant="secondary" disabled>
          Next page
        </Button>
      ) : null}
    </section>
  );
}

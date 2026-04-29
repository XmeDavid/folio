"use client";

import Link from "next/link";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DataTable } from "@/components/ui/data-table";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState } from "@/components/app/empty";
import { InviteUserDialog } from "@/components/admin/invite-user-dialog";
import {
  useAdminUsers,
  useAdminInvites,
  useRevokeAdminInvite,
  type PlatformInvite,
} from "@/lib/admin/client";
import { formatDate } from "@/lib/format";

type AdminUser = NonNullable<
  ReturnType<typeof useAdminUsers>["data"]
>["data"][number];

export default function AdminUsersPage() {
  const [search, setSearch] = useState("");
  const [isAdminOnly, setIsAdminOnly] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const q = useAdminUsers({ search, isAdminOnly });
  const invitesQ = useAdminInvites();
  const revoke = useRevokeAdminInvite();

  const invites = invitesQ.data ?? [];
  const showInvitesEmpty = !invitesQ.isLoading && invites.length === 0;

  return (
    <section className="flex flex-col gap-5">
      <PageHeader
        title="Users"
        description="Account, session, and admin status."
        actions={
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-2 text-[13px] text-fg-muted">
              <input
                type="checkbox"
                checked={isAdminOnly}
                onChange={(e) => setIsAdminOnly(e.target.checked)}
              />
              Admins only
            </label>
            <Button onClick={() => setInviteOpen(true)}>Invite user</Button>
          </div>
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

      <section className="flex flex-col gap-3">
        <h2 className="text-[15px] font-medium text-fg">Pending invites</h2>
        {showInvitesEmpty ? (
          <EmptyState
            title="No pending invites"
            description="Click 'Invite user' to send one."
          />
        ) : (
          <DataTable<PlatformInvite>
            columns={[
              {
                header: "Email",
                cell: (i) =>
                  i.email ?? (
                    <span className="text-fg-muted">Open invite</span>
                  ),
              },
              { header: "Created", cell: (i) => formatDate(i.createdAt) },
              { header: "Expires", cell: (i) => formatDate(i.expiresAt) },
              {
                header: "",
                align: "right",
                cell: (i) => (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => revoke.mutate(i.id)}
                    disabled={revoke.isPending}
                    className="text-danger hover:bg-[#F5DADA]"
                  >
                    Revoke
                  </Button>
                ),
              },
            ]}
            rows={invites}
            rowKey={(i) => i.id}
            isLoading={invitesQ.isLoading}
          />
        )}
      </section>

      <InviteUserDialog
        open={inviteOpen}
        onClose={() => setInviteOpen(false)}
      />
    </section>
  );
}

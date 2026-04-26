"use client";

import { use, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import {
  ApiError,
  getMembers,
  revokeInvite,
  type PendingInvite,
} from "@/lib/api/client";
import { NewInviteDialog } from "@/components/invites/new-invite-dialog";

export default function InvitesSettingsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const qc = useQueryClient();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [rowError, setRowError] = useState<{
    id: string;
    message: string;
  } | null>(null);

  const query = useQuery({
    queryKey: ["members", workspace?.id],
    queryFn: () => getMembers(workspace!.id),
    enabled: !!workspace,
  });

  const revoke = useMutation({
    mutationFn: (inviteId: string) => revokeInvite(workspace!.id, inviteId),
    onSuccess: async () => {
      setRowError(null);
      await qc.invalidateQueries({ queryKey: ["members", workspace!.id] });
    },
    onError: (err, inviteId) => {
      setRowError({ id: inviteId, message: formatError(err) });
    },
  });

  if (!workspace) return null;

  const isOwner = workspace.role === "owner";
  const invites = query.data?.pendingInvites ?? [];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Invites</h1>
          <p className="text-sm text-muted-foreground">
            Pending invitations to join this workspace.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setDialogOpen(true)}
          className="rounded bg-foreground px-3 py-2 text-sm text-background"
        >
          New invite
        </button>
      </div>

      {query.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : query.isError ? (
        <p className="text-sm text-red-600">{formatError(query.error)}</p>
      ) : (
        <div className="overflow-hidden rounded border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">Email</th>
                <th className="px-3 py-2 font-medium">Role</th>
                <th className="px-3 py-2 font-medium">Invited</th>
                <th className="px-3 py-2 font-medium">Expires</th>
                <th className="px-3 py-2 font-medium">Action</th>
              </tr>
            </thead>
            <tbody>
              {invites.map((inv) => (
                <InviteRow
                  key={inv.id}
                  invite={inv}
                  onRevoke={() => revoke.mutate(inv.id)}
                  pending={revoke.isPending && revoke.variables === inv.id}
                  errorMessage={
                    rowError?.id === inv.id ? rowError.message : null
                  }
                />
              ))}
              {invites.length === 0 ? (
                <tr>
                  <td
                    colSpan={5}
                    className="px-3 py-4 text-center text-muted-foreground"
                  >
                    No pending invites.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      )}

      <NewInviteDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        workspaceId={workspace.id}
        canInviteOwners={isOwner}
      />
    </div>
  );
}

function InviteRow({
  invite,
  onRevoke,
  pending,
  errorMessage,
}: {
  invite: PendingInvite;
  onRevoke: () => void;
  pending: boolean;
  errorMessage: string | null;
}) {
  return (
    <>
      <tr className="border-t">
        <td className="px-3 py-2">{invite.email}</td>
        <td className="px-3 py-2">{invite.role}</td>
        <td className="px-3 py-2 text-muted-foreground">
          {formatDate(invite.invitedAt)}
        </td>
        <td className="px-3 py-2 text-muted-foreground">
          {formatDate(invite.expiresAt)}
        </td>
        <td className="px-3 py-2">
          <button
            type="button"
            disabled={pending}
            onClick={onRevoke}
            className="rounded border border-red-400 px-2 py-1 text-red-700 hover:bg-red-50 disabled:opacity-60"
          >
            {pending ? "…" : "Revoke"}
          </button>
        </td>
      </tr>
      {errorMessage ? (
        <tr>
          <td
            colSpan={5}
            className="border-t bg-red-50 px-3 py-2 text-sm text-red-700"
          >
            {errorMessage}
          </td>
        </tr>
      ) : null}
    </>
  );
}

function formatDate(value: string): string {
  try {
    return new Date(value).toLocaleDateString();
  } catch {
    return value;
  }
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403 && err.body?.code === "reauth_required") {
      return "Re-authentication required. Please sign in again.";
    }
    return err.body?.error ?? err.message;
  }
  return (err as Error)?.message ?? "Request failed";
}

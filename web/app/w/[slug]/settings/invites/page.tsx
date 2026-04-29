"use client";

import { use, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import {
  ApiError,
  getMembers,
  resendInvite,
  revokeInvite,
  type PendingInvite,
} from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";
import { NewInviteDialog } from "@/components/invites/new-invite-dialog";

// Auto-clear the inline resend success panel after this many ms. Long enough
// for the inviter to copy the link, short enough that a stale URL doesn't
// linger on screen indefinitely.
const RESEND_PANEL_TTL_MS = 30_000;

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
  // Per-row resend success — at most one panel visible at a time so the page
  // stays scannable. Auto-clears via the timer in InviteRow.
  const [resendSuccess, setResendSuccess] = useState<{
    id: string;
    acceptUrl: string;
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
      setResendSuccess(null);
      await qc.invalidateQueries({ queryKey: ["members", workspace!.id] });
    },
    onError: (err, inviteId) => {
      setRowError({ id: inviteId, message: formatError(err) });
    },
  });

  const resend = useMutation({
    mutationFn: (inviteId: string) => resendInvite(workspace!.id, inviteId),
    onSuccess: async (data, inviteId) => {
      setRowError(null);
      setResendSuccess({ id: inviteId, acceptUrl: data.acceptUrl });
      // The new expiry pushes the row's "Expires" column out — refresh.
      await qc.invalidateQueries({ queryKey: ["members", workspace!.id] });
    },
    onError: (err, inviteId) => {
      setResendSuccess(null);
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
                <th className="px-3 py-2 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {invites.map((inv) => (
                <InviteRow
                  key={inv.id}
                  invite={inv}
                  onRevoke={() => revoke.mutate(inv.id)}
                  onResend={() => resend.mutate(inv.id)}
                  revokePending={
                    revoke.isPending && revoke.variables === inv.id
                  }
                  resendPending={
                    resend.isPending && resend.variables === inv.id
                  }
                  errorMessage={
                    rowError?.id === inv.id ? rowError.message : null
                  }
                  successUrl={
                    resendSuccess?.id === inv.id
                      ? resendSuccess.acceptUrl
                      : null
                  }
                  onDismissSuccess={() => setResendSuccess(null)}
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
  onResend,
  revokePending,
  resendPending,
  errorMessage,
  successUrl,
  onDismissSuccess,
}: {
  invite: PendingInvite;
  onRevoke: () => void;
  onResend: () => void;
  revokePending: boolean;
  resendPending: boolean;
  errorMessage: string | null;
  successUrl: string | null;
  onDismissSuccess: () => void;
}) {
  const busy = revokePending || resendPending;

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
          <div className="flex gap-2">
            <button
              type="button"
              disabled={busy}
              onClick={onResend}
              className="rounded border border-border px-2 py-1 text-foreground hover:bg-muted/50 disabled:opacity-60"
            >
              {resendPending ? "…" : "Resend"}
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={onRevoke}
              className="rounded border border-red-400 px-2 py-1 text-red-700 hover:bg-red-50 disabled:opacity-60"
            >
              {revokePending ? "…" : "Revoke"}
            </button>
          </div>
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
      {successUrl ? (
        <tr>
          <td colSpan={5} className="border-t bg-emerald-50 px-3 py-3">
            <ResendSuccessPanel url={successUrl} onDismiss={onDismissSuccess} />
          </td>
        </tr>
      ) : null}
    </>
  );
}

// ResendSuccessPanel surfaces the freshly-rotated accept URL inline under
// the row so the inviter can copy the link without leaving the page. Auto-
// clears after RESEND_PANEL_TTL_MS so a stale URL doesn't linger.
function ResendSuccessPanel({
  url,
  onDismiss,
}: {
  url: string;
  onDismiss: () => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);

  // Auto-dismiss timer. Re-arms whenever a fresh `url` lands so a second
  // resend on a different row resets the countdown.
  useEffect(() => {
    const t = window.setTimeout(onDismiss, RESEND_PANEL_TTL_MS);
    return () => window.clearTimeout(t);
  }, [url, onDismiss]);

  // Reset the "Copied" affordance after 2s.
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
    } catch {
      inputRef.current?.select();
    }
  };

  return (
    <div className="flex flex-col gap-2 text-sm text-emerald-900">
      <p>
        New accept link issued. Send it to the invitee. We've also re-sent the
        invite email best-effort.
      </p>
      <div className="flex gap-2">
        <input
          ref={inputRef}
          readOnly
          value={url}
          onFocus={(e) => e.currentTarget.select()}
          className="w-full rounded border border-emerald-300 bg-white px-2 py-1 font-mono text-[12px] text-foreground"
        />
        <button
          type="button"
          onClick={handleCopy}
          className="shrink-0 rounded border border-emerald-400 px-2 py-1 text-emerald-800 hover:bg-emerald-100"
        >
          {copied ? "Copied" : "Copy link"}
        </button>
        <button
          type="button"
          onClick={onDismiss}
          className="shrink-0 rounded border border-emerald-400 px-2 py-1 text-emerald-800 hover:bg-emerald-100"
        >
          Dismiss
        </button>
      </div>
    </div>
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
    return friendlyError(err.body?.code, err.body?.error ?? err.message);
  }
  return (err as Error)?.message ?? "Request failed";
}

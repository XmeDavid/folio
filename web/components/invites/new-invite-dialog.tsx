"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  createInvite,
  type MemberRole,
} from "@/lib/api/client";

export function NewInviteDialog({
  open,
  onClose,
  tenantId,
  canInviteOwners,
}: {
  open: boolean;
  onClose: () => void;
  tenantId: string;
  canInviteOwners: boolean;
}) {
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<MemberRole>("member");
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => createInvite(tenantId, { email, role }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["members", tenantId] });
      setEmail("");
      setRole("member");
      setError(null);
      onClose();
    },
    onError: (err) => {
      setError(formatError(err));
    },
  });

  if (!open) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={() => {
        if (!create.isPending) onClose();
      }}
    >
      <div
        className="w-full max-w-md rounded bg-background p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="text-lg font-semibold">New invite</h3>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
          className="mt-3 flex flex-col gap-3"
        >
          <label className="flex flex-col gap-1">
            <span className="text-sm font-medium">Email</span>
            <input
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="rounded border px-3 py-2"
              autoFocus
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-sm font-medium">Role</span>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value as MemberRole)}
              className="rounded border px-3 py-2"
            >
              <option value="member">member</option>
              {canInviteOwners ? (
                <option value="owner">owner</option>
              ) : null}
            </select>
            {!canInviteOwners ? (
              <span className="text-xs text-muted-foreground">
                Only owners can invite other owners.
              </span>
            ) : null}
          </label>
          {error ? <p className="text-sm text-red-600">{error}</p> : null}
          <div className="mt-2 flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={create.isPending}
              className="rounded border px-3 py-2 text-sm"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={create.isPending}
              className="rounded bg-foreground px-3 py-2 text-sm text-background disabled:opacity-60"
            >
              {create.isPending ? "Sending…" : "Send invite"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
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

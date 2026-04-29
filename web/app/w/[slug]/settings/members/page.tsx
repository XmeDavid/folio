"use client";

import { use, useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentWorkspace, useIdentity } from "@/lib/hooks/use-identity";
import {
  ApiError,
  getMembers,
  patchMember,
  removeMember,
  type MemberRole,
  type MemberWithUser,
} from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";

export default function MembersSettingsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const identity = useIdentity();
  const me = identity.status === "authenticated" ? identity.data.user : null;
  const router = useRouter();
  const qc = useQueryClient();

  const [rowError, setRowError] = useState<{
    userId: string;
    message: string;
  } | null>(null);

  const membersQuery = useQuery({
    queryKey: ["members", workspace?.id],
    queryFn: () => getMembers(workspace!.id),
    enabled: !!workspace,
  });

  const patchRole = useMutation({
    mutationFn: (args: { userId: string; role: MemberRole }) =>
      patchMember(workspace!.id, args.userId, args.role),
    onSuccess: async () => {
      setRowError(null);
      await qc.invalidateQueries({ queryKey: ["members", workspace!.id] });
      await qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (err, vars) => {
      setRowError({ userId: vars.userId, message: formatError(err) });
    },
  });

  const remove = useMutation({
    mutationFn: (userId: string) => removeMember(workspace!.id, userId),
    onSuccess: async (_data, userId) => {
      setRowError(null);
      if (me && userId === me.id) {
        await qc.invalidateQueries({ queryKey: ["me"] });
        router.push("/workspaces" as Route);
        return;
      }
      await qc.invalidateQueries({ queryKey: ["members", workspace!.id] });
    },
    onError: (err, userId) => {
      setRowError({ userId, message: formatError(err) });
    },
  });

  if (!workspace) return null;

  const isOwner = workspace.role === "owner";
  const members = membersQuery.data?.members ?? [];

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold">Members</h1>
        <p className="text-sm text-muted-foreground">
          {isOwner
            ? "Change roles, remove members, or leave this workspace."
            : "You can leave this workspace. Only owners can change roles."}
        </p>
      </div>

      {membersQuery.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : membersQuery.isError ? (
        <p className="text-sm text-red-600">
          {formatError(membersQuery.error)}
        </p>
      ) : (
        <div className="overflow-hidden rounded border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">Name</th>
                <th className="px-3 py-2 font-medium">Email</th>
                <th className="px-3 py-2 font-medium">Role</th>
                <th className="px-3 py-2 font-medium">Action</th>
              </tr>
            </thead>
            <tbody>
              {members.map((m) => (
                <MemberRow
                  key={m.userId}
                  member={m}
                  isOwner={isOwner}
                  isSelf={!!me && me.id === m.userId}
                  pendingPatch={
                    patchRole.isPending && patchRole.variables?.userId === m.userId
                  }
                  pendingRemove={
                    remove.isPending && remove.variables === m.userId
                  }
                  onRoleChange={(role) =>
                    patchRole.mutate({ userId: m.userId, role })
                  }
                  onRemove={() => remove.mutate(m.userId)}
                  errorMessage={
                    rowError?.userId === m.userId ? rowError.message : null
                  }
                />
              ))}
              {members.length === 0 ? (
                <tr>
                  <td
                    colSpan={4}
                    className="px-3 py-4 text-center text-muted-foreground"
                  >
                    No members.
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function MemberRow({
  member,
  isOwner,
  isSelf,
  pendingPatch,
  pendingRemove,
  onRoleChange,
  onRemove,
  errorMessage,
}: {
  member: MemberWithUser;
  isOwner: boolean;
  isSelf: boolean;
  pendingPatch: boolean;
  pendingRemove: boolean;
  onRoleChange: (role: MemberRole) => void;
  onRemove: () => void;
  errorMessage: string | null;
}) {
  const actionLabel = isSelf
    ? "Leave"
    : isOwner
      ? "Remove"
      : null;

  return (
    <>
      <tr className="border-t">
        <td className="px-3 py-2">{member.displayName}</td>
        <td className="px-3 py-2 text-muted-foreground">{member.email}</td>
        <td className="px-3 py-2">
          {isOwner ? (
            <select
              value={member.role}
              disabled={pendingPatch}
              onChange={(e) =>
                onRoleChange(e.target.value as MemberRole)
              }
              className="rounded border px-2 py-1"
            >
              <option value="owner">owner</option>
              <option value="member">member</option>
            </select>
          ) : (
            <span>{member.role}</span>
          )}
        </td>
        <td className="px-3 py-2">
          {actionLabel ? (
            <button
              type="button"
              disabled={pendingRemove}
              onClick={onRemove}
              className="rounded border border-red-400 px-2 py-1 text-red-700 hover:bg-red-50 disabled:opacity-60"
            >
              {pendingRemove ? "…" : actionLabel}
            </button>
          ) : null}
        </td>
      </tr>
      {errorMessage ? (
        <tr>
          <td colSpan={4} className="border-t bg-red-50 px-3 py-2 text-sm text-red-700">
            {errorMessage}
          </td>
        </tr>
      ) : null}
    </>
  );
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    return friendlyError(err.body?.code, err.body?.error ?? err.message);
  }
  return (err as Error)?.message ?? "Request failed";
}

"use client";

import { use, useEffect } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  acceptInvite,
  previewInvite,
  fetchMe,
  type InvitePreview,
} from "@/lib/api/client";
import { useIdentity, type Me } from "@/lib/hooks/use-identity";

export default function AcceptInvitePage({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = use(params);
  const router = useRouter();
  const identity = useIdentity();
  const qc = useQueryClient();

  const preview = useQuery<InvitePreview, ApiError>({
    queryKey: ["invitePreview", token],
    queryFn: () => previewInvite(token),
    retry: false,
  });

  const accept = useMutation<unknown, unknown, void>({
    mutationFn: () => acceptInvite(token),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["me"] });
      const fresh = await qc.fetchQuery<Me>({
        queryKey: ["me"],
        queryFn: fetchMe,
      });
      const tenantId = preview.data?.tenantId;
      const target = fresh.tenants.find((t) => t.id === tenantId);
      if (target) {
        router.push(`/t/${target.slug}` as Route);
      } else {
        router.push("/tenants" as Route);
      }
    },
  });

  // Redirect unauthenticated visitors to /signup with invite context.
  useEffect(() => {
    if (identity.status !== "unauthenticated") return;
    if (!preview.data) return;
    const search = new URLSearchParams({
      inviteToken: token,
      email: preview.data.email,
    });
    router.replace(`/signup?${search.toString()}` as Route);
  }, [identity.status, preview.data, router, token]);

  if (preview.isLoading || identity.status === "loading") {
    return (
      <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center p-6">
        <p className="text-sm text-muted-foreground">Loading invite…</p>
      </main>
    );
  }

  if (preview.isError) {
    return (
      <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 p-6">
        <h1 className="text-2xl font-semibold">Invite unavailable</h1>
        <p className="text-sm text-red-600">
          {formatError(preview.error)}
        </p>
        <a href="/login" className="text-sm underline">
          Back to sign in
        </a>
      </main>
    );
  }

  if (!preview.data) return null;

  // Unauthenticated — useEffect above handles the redirect. Show a stub.
  if (identity.status === "unauthenticated") {
    return (
      <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center p-6">
        <p className="text-sm text-muted-foreground">Redirecting to signup…</p>
      </main>
    );
  }

  const invite = preview.data;
  const me = identity.data.user;
  const emailMatches = me.email.toLowerCase() === invite.email.toLowerCase();

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold">Join {invite.tenantName}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {invite.inviterDisplayName} invited <strong>{invite.email}</strong>{" "}
          to join <strong>{invite.tenantName}</strong> as{" "}
          <strong>{invite.role}</strong>.
        </p>
      </div>

      {emailMatches ? (
        <>
          {accept.isError ? (
            <p className="text-sm text-red-600">
              {formatError(accept.error)}
            </p>
          ) : null}
          <button
            type="button"
            disabled={accept.isPending}
            onClick={() => accept.mutate()}
            className="rounded bg-foreground px-3 py-2 text-background disabled:opacity-60"
          >
            {accept.isPending ? "Joining…" : `Join ${invite.tenantName}`}
          </button>
        </>
      ) : (
        <div className="rounded border border-amber-300 bg-amber-50 p-4 text-sm">
          <p className="font-medium text-amber-900">
            Signed in as the wrong account
          </p>
          <p className="mt-1 text-amber-900">
            This invite is for <strong>{invite.email}</strong>, but you are
            signed in as <strong>{me.email}</strong>. Sign out and sign in as{" "}
            <strong>{invite.email}</strong> to accept the invite.
          </p>
          <a
            href="/login"
            className="mt-3 inline-block underline"
          >
            Go to sign in
          </a>
        </div>
      )}
    </main>
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

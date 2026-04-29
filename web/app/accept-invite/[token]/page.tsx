"use client";

import { use, useEffect } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  acceptInvite,
  logout,
  previewInvite,
  previewPlatformInvite,
  fetchMe,
  type InvitePreview,
  type PlatformInvitePreview,
} from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";
import { useIdentity, type Me } from "@/lib/hooks/use-identity";

type Resolved =
  | { kind: "workspace"; data: InvitePreview }
  | { kind: "platform"; data: PlatformInvitePreview };

export default function AcceptInvitePage({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = use(params);
  const router = useRouter();
  const identity = useIdentity();
  const qc = useQueryClient();

  const preview = useQuery<Resolved, ApiError>({
    queryKey: ["invitePreview", token],
    queryFn: async () => {
      try {
        const data = await previewInvite(token);
        return { kind: "workspace", data };
      } catch (err) {
        if (
          err instanceof ApiError &&
          (err.status === 404 || err.status === 410)
        ) {
          const data = await previewPlatformInvite(token);
          return { kind: "platform", data };
        }
        throw err;
      }
    },
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
      const workspaceId =
        preview.data?.kind === "workspace"
          ? preview.data.data.workspaceId
          : undefined;
      const target = fresh.workspaces.find((t) => t.id === workspaceId);
      if (target) {
        router.push(`/w/${target.slug}` as Route);
      } else {
        router.push("/workspaces" as Route);
      }
    },
  });

  const signOut = useMutation<unknown, unknown, void>({
    mutationFn: logout,
    onSettled: async () => {
      qc.setQueryData(["me"], undefined);
      await qc.invalidateQueries({ queryKey: ["me"] });
      router.replace(`/accept-invite/${token}` as Route);
    },
  });

  // Redirect unauthenticated visitors to /signup with invite context.
  useEffect(() => {
    if (identity.status !== "unauthenticated") return;
    if (!preview.data) return;
    const params = new URLSearchParams({ inviteToken: token });
    const inviteEmail =
      preview.data.kind === "workspace"
        ? preview.data.data.email
        : preview.data.data.email;
    if (inviteEmail) {
      params.set("email", inviteEmail);
    }
    router.replace(`/signup?${params.toString()}` as Route);
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

  // Platform invite — authenticated user. Friendly already-signed-in card.
  if (preview.data.kind === "platform") {
    const me = identity.data.user;
    return (
      <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-6 p-6">
        <div>
          <h1 className="text-2xl font-semibold">You&rsquo;re already signed in</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Platform invites are for new accounts. You&rsquo;re currently signed
            in as <strong>{me.email}</strong>. Sign out to use this invite, or
            just continue using your existing workspace.
          </p>
        </div>
        {signOut.isError ? (
          <p className="text-sm text-red-600">
            {formatError(signOut.error)}
          </p>
        ) : null}
        <div className="flex flex-col gap-2">
          <a
            href="/workspaces"
            className="rounded bg-foreground px-3 py-2 text-center text-background"
          >
            Go to my workspaces
          </a>
          <button
            type="button"
            disabled={signOut.isPending}
            onClick={() => signOut.mutate()}
            className="rounded border px-3 py-2 disabled:opacity-60"
          >
            {signOut.isPending ? "Signing out…" : "Sign out"}
          </button>
        </div>
      </main>
    );
  }

  // Workspace invite — authenticated user.
  const invite = preview.data.data;
  const me = identity.data.user;
  const emailMatches = me.email.toLowerCase() === invite.email.toLowerCase();

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold">Join {invite.workspaceName}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {invite.inviterDisplayName} invited <strong>{invite.email}</strong>{" "}
          to join <strong>{invite.workspaceName}</strong> as{" "}
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
            {accept.isPending ? "Joining…" : `Join ${invite.workspaceName}`}
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
    return friendlyError(err.body?.code, err.body?.error ?? err.message);
  }
  return (err as Error)?.message ?? "Request failed";
}

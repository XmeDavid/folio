"use client";

import { use, useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import {
  ApiError,
  deleteWorkspace,
  patchWorkspace,
  type WorkspacePatchInput,
} from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";

export default function WorkspaceSettingsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const router = useRouter();
  const qc = useQueryClient();

  const [draft, setDraft] = useState<{
    key: string;
    name: string;
    slug: string;
    baseCurrency: string;
    cycleAnchorDay: number;
  } | null>(null);

  const workspaceKey = workspace
    ? `${workspace.id}:${workspace.name}:${workspace.slug}:${workspace.baseCurrency}:${workspace.cycleAnchorDay}`
    : "";

  // Sync the local draft when the workspace snapshot we're editing changes.
  if (workspace && draft?.key !== workspaceKey) {
    setDraft({
      key: workspaceKey,
      name: workspace.name,
      slug: workspace.slug,
      baseCurrency: workspace.baseCurrency,
      cycleAnchorDay: workspace.cycleAnchorDay,
    });
  }

  const [formError, setFormError] = useState<string | null>(null);
  const [formOk, setFormOk] = useState<string | null>(null);

  const [dangerOpen, setDangerOpen] = useState(false);
  const [confirmSlug, setConfirmSlug] = useState("");
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const isOwner = workspace?.role === "owner";

  const patch = useMutation({
    mutationFn: (body: WorkspacePatchInput) => patchWorkspace(workspace!.id, body),
    onSuccess: async (updated) => {
      setFormError(null);
      setFormOk("Saved");
      await qc.invalidateQueries({ queryKey: ["me"] });
      if (updated.slug !== slug) {
        router.replace(`/w/${updated.slug}/settings/workspace` as Route);
      }
    },
    onError: (err) => {
      setFormOk(null);
      setFormError(formatError(err));
    },
  });

  const del = useMutation({
    mutationFn: () => deleteWorkspace(workspace!.id),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["me"] });
      router.push("/workspaces" as Route);
    },
    onError: (err) => {
      setDeleteError(formatError(err));
    },
  });

  if (!workspace || !draft) return null;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!workspace || !draft) return;
    const body: WorkspacePatchInput = {};
    if (draft.name !== workspace.name) body.name = draft.name;
    if (draft.slug !== workspace.slug) body.slug = draft.slug;
    if (draft.baseCurrency !== workspace.baseCurrency)
      body.baseCurrency = draft.baseCurrency.toUpperCase();
    if (draft.cycleAnchorDay !== workspace.cycleAnchorDay)
      body.cycleAnchorDay = draft.cycleAnchorDay;
    if (Object.keys(body).length === 0) {
      setFormError(null);
      setFormOk("Nothing to save");
      return;
    }
    patch.mutate(body);
  }

  return (
    <div className="flex max-w-xl flex-col gap-8">
      <div>
        <h1 className="text-2xl font-semibold">Workspace settings</h1>
        <p className="text-sm text-muted-foreground">
          {isOwner
            ? "Update the workspace's identity and financial defaults."
            : "Only owners can change workspace settings."}
        </p>
      </div>

      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Name">
          <input
            type="text"
            value={draft.name}
            onChange={(e) =>
              setDraft({ ...draft, name: e.target.value })
            }
            disabled={!isOwner}
            className="w-full rounded border px-3 py-2 disabled:opacity-60"
          />
        </Field>
        <Field
          label="Slug"
          hint="URL-safe identifier used in /t/{slug} routes."
        >
          <input
            type="text"
            value={draft.slug}
            onChange={(e) =>
              setDraft({ ...draft, slug: e.target.value })
            }
            disabled={!isOwner}
            className="w-full rounded border px-3 py-2 disabled:opacity-60"
          />
        </Field>
        <Field label="Base currency">
          <input
            type="text"
            value={draft.baseCurrency}
            onChange={(e) =>
              setDraft({ ...draft, baseCurrency: e.target.value.toUpperCase() })
            }
            disabled={!isOwner}
            maxLength={3}
            className="w-full rounded border px-3 py-2 uppercase disabled:opacity-60"
          />
        </Field>
        <Field
          label="Cycle anchor day"
          hint="Day of the month (1-28) used to anchor monthly cycles."
        >
          <input
            type="number"
            min={1}
            max={28}
            value={draft.cycleAnchorDay}
            onChange={(e) =>
              setDraft({
                ...draft,
                cycleAnchorDay: Number(e.target.value) || 1,
              })
            }
            disabled={!isOwner}
            className="w-full rounded border px-3 py-2 disabled:opacity-60"
          />
        </Field>

        {formError ? (
          <p className="text-sm text-red-600">{formError}</p>
        ) : null}
        {formOk ? <p className="text-sm text-green-700">{formOk}</p> : null}

        <div>
          <button
            type="submit"
            disabled={!isOwner || patch.isPending}
            className="rounded bg-foreground px-3 py-2 text-background disabled:opacity-60"
          >
            {patch.isPending ? "Saving…" : "Save changes"}
          </button>
        </div>
      </form>

      {isOwner ? (
        <section className="rounded border border-red-300 p-4">
          <h2 className="text-lg font-semibold text-red-700">Danger zone</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Soft-delete this workspace. You can restore it later from the workspaces
            list.
          </p>
          <button
            type="button"
            onClick={() => {
              setDangerOpen(true);
              setConfirmSlug("");
              setDeleteError(null);
            }}
            className="mt-3 rounded border border-red-400 px-3 py-2 text-sm text-red-700 hover:bg-red-50"
          >
            Delete workspace
          </button>
        </section>
      ) : null}

      {dangerOpen ? (
        <div
          role="dialog"
          aria-modal="true"
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
          onClick={() => {
            if (!del.isPending) setDangerOpen(false);
          }}
        >
          <div
            className="w-full max-w-md rounded bg-background p-6 shadow-lg"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-lg font-semibold">Delete workspace</h3>
            <p className="mt-2 text-sm text-muted-foreground">
              This soft-deletes the workspace. Type{" "}
              <code className="rounded bg-muted px-1">{workspace.slug}</code> to
              confirm.
            </p>
            <input
              type="text"
              value={confirmSlug}
              onChange={(e) => setConfirmSlug(e.target.value)}
              placeholder="workspace slug"
              className="mt-3 w-full rounded border px-3 py-2"
              autoFocus
            />
            {deleteError ? (
              <p className="mt-2 text-sm text-red-600">{deleteError}</p>
            ) : null}
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setDangerOpen(false)}
                disabled={del.isPending}
                className="rounded border px-3 py-2 text-sm"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={confirmSlug !== workspace.slug || del.isPending}
                onClick={() => del.mutate()}
                className="rounded bg-red-600 px-3 py-2 text-sm text-white disabled:opacity-60"
              >
                {del.isPending ? "Deleting…" : "Delete workspace"}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </label>
  );
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    return friendlyError(err.body?.code, err.body?.error ?? err.message);
  }
  return (err as Error)?.message ?? "Request failed";
}

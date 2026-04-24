"use client";

import { use, useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCurrentTenant } from "@/lib/hooks/use-identity";
import {
  ApiError,
  deleteTenant,
  patchTenant,
  type TenantPatchInput,
} from "@/lib/api/client";

export default function TenantSettingsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const tenant = useCurrentTenant(slug);
  const router = useRouter();
  const qc = useQueryClient();

  const [draft, setDraft] = useState<{
    key: string;
    name: string;
    slug: string;
    baseCurrency: string;
    cycleAnchorDay: number;
  } | null>(null);

  const tenantKey = tenant
    ? `${tenant.id}:${tenant.name}:${tenant.slug}:${tenant.baseCurrency}:${tenant.cycleAnchorDay}`
    : "";

  // Sync the local draft when the tenant snapshot we're editing changes.
  if (tenant && draft?.key !== tenantKey) {
    setDraft({
      key: tenantKey,
      name: tenant.name,
      slug: tenant.slug,
      baseCurrency: tenant.baseCurrency,
      cycleAnchorDay: tenant.cycleAnchorDay,
    });
  }

  const [formError, setFormError] = useState<string | null>(null);
  const [formOk, setFormOk] = useState<string | null>(null);

  const [dangerOpen, setDangerOpen] = useState(false);
  const [confirmSlug, setConfirmSlug] = useState("");
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const isOwner = tenant?.role === "owner";

  const patch = useMutation({
    mutationFn: (body: TenantPatchInput) => patchTenant(tenant!.id, body),
    onSuccess: async (updated) => {
      setFormError(null);
      setFormOk("Saved");
      await qc.invalidateQueries({ queryKey: ["me"] });
      if (updated.slug !== slug) {
        router.replace(`/t/${updated.slug}/settings/tenant` as Route);
      }
    },
    onError: (err) => {
      setFormOk(null);
      setFormError(formatError(err));
    },
  });

  const del = useMutation({
    mutationFn: () => deleteTenant(tenant!.id),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["me"] });
      router.push("/tenants" as Route);
    },
    onError: (err) => {
      setDeleteError(formatError(err));
    },
  });

  if (!tenant || !draft) return null;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!tenant || !draft) return;
    const body: TenantPatchInput = {};
    if (draft.name !== tenant.name) body.name = draft.name;
    if (draft.slug !== tenant.slug) body.slug = draft.slug;
    if (draft.baseCurrency !== tenant.baseCurrency)
      body.baseCurrency = draft.baseCurrency.toUpperCase();
    if (draft.cycleAnchorDay !== tenant.cycleAnchorDay)
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
        <h1 className="text-2xl font-semibold">Tenant settings</h1>
        <p className="text-sm text-muted-foreground">
          {isOwner
            ? "Update the tenant's identity and financial defaults."
            : "Only owners can change tenant settings."}
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
            Soft-delete this tenant. You can restore it later from the tenants
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
            Delete tenant
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
            <h3 className="text-lg font-semibold">Delete tenant</h3>
            <p className="mt-2 text-sm text-muted-foreground">
              This soft-deletes the tenant. Type{" "}
              <code className="rounded bg-muted px-1">{tenant.slug}</code> to
              confirm.
            </p>
            <input
              type="text"
              value={confirmSlug}
              onChange={(e) => setConfirmSlug(e.target.value)}
              placeholder="tenant slug"
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
                disabled={confirmSlug !== tenant.slug || del.isPending}
                onClick={() => del.mutate()}
                className="rounded bg-red-600 px-3 py-2 text-sm text-white disabled:opacity-60"
              >
                {del.isPending ? "Deleting…" : "Delete tenant"}
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
    if (err.status === 403 && err.body?.code === "reauth_required") {
      return "Re-authentication required. Please sign in again.";
    }
    return err.body?.error ?? err.message;
  }
  return (err as Error)?.message ?? "Request failed";
}

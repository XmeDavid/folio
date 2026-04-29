"use client";

import * as React from "react";
import Link from "next/link";
import type { Route } from "next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArchiveRestore,
  Check,
  Pencil,
  X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { FormError } from "@/components/ui/form-error";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { MerchantDefaultCategoryDialog } from "@/components/classification/merchant-default-category-dialog";
import {
  ApiError,
  archiveMerchant,
  createMerchant,
  updateMerchant,
  type Category,
  type Merchant,
  type MerchantPatchResult,
} from "@/lib/api/client";

export function MerchantsTable({
  slug,
  workspaceId,
  merchants,
  categoryById,
  leafCategories,
}: {
  slug: string;
  workspaceId: string;
  merchants: Merchant[];
  categoryById: Map<string, Category>;
  leafCategories: Category[];
}) {
  return (
    <Card className="overflow-hidden">
      <div className="hidden grid-cols-[1fr_220px_120px_120px] items-center gap-4 border-b border-border px-5 py-2 text-[11px] font-medium tracking-[0.07em] uppercase text-fg-faint md:grid">
        <span>Merchant</span>
        <span>Default category</span>
        <span>Status</span>
        <span className="text-right">Actions</span>
      </div>
      <ul className="divide-y divide-border">
        {merchants.map((merchant) => (
          <MerchantRow
            key={merchant.id}
            slug={slug}
            workspaceId={workspaceId}
            merchant={merchant}
            categoryById={categoryById}
            leafCategories={leafCategories}
          />
        ))}
      </ul>
    </Card>
  );
}

function MerchantRow({
  slug,
  workspaceId,
  merchant,
  categoryById,
  leafCategories,
}: {
  slug: string;
  workspaceId: string;
  merchant: Merchant;
  categoryById: Map<string, Category>;
  leafCategories: Category[];
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = React.useState(false);

  const archiveMutation = useMutation({
    mutationFn: async () => {
      if (merchant.archivedAt) {
        // No dedicated unarchive endpoint; PATCH with `archived: false`.
        await updateMerchant(workspaceId, merchant.id, { archived: false });
      } else {
        await archiveMerchant(workspaceId, merchant.id);
      }
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["merchants", workspaceId],
      });
    },
  });

  if (editing) {
    return (
      <li className="px-5 py-3">
        <MerchantForm
          slug={slug}
          workspaceId={workspaceId}
          leafCategories={leafCategories}
          merchant={merchant}
          // The list view doesn't have a transaction count; pass 0 so we
          // never prompt the cascade dialog from here. Default-category
          // changes that should re-categorise existing transactions are
          // expected to happen on the merchant detail page where the count
          // is known.
          transactionCount={0}
          onDone={() => setEditing(false)}
          onCancel={() => setEditing(false)}
        />
      </li>
    );
  }

  const defaultCategory = merchant.defaultCategoryId
    ? categoryById.get(merchant.defaultCategoryId)
    : null;

  return (
    <li className="grid grid-cols-1 gap-3 px-5 py-3 md:grid-cols-[1fr_220px_120px_120px] md:items-center md:gap-4">
      <div className="flex min-w-0 items-center gap-3">
        <MerchantAvatar logoUrl={merchant.logoUrl} />
        <div className="min-w-0">
          <Link
            href={`/w/${slug}/merchants/${merchant.id}` as Route}
            className="block truncate text-[14px] font-medium text-fg hover:underline"
          >
            {merchant.canonicalName}
          </Link>
          {merchant.industry ? (
            <div className="truncate text-[12px] text-fg-faint">
              {merchant.industry}
            </div>
          ) : null}
        </div>
      </div>
      <span className="truncate text-[13px] text-fg-muted">
        {defaultCategory ? (
          defaultCategory.name
        ) : (
          <span className="text-fg-faint">— none —</span>
        )}
      </span>
      <span>
        {merchant.archivedAt ? (
          <Badge variant="neutral">Archived</Badge>
        ) : (
          <Badge variant="success">Active</Badge>
        )}
      </span>
      <div className="flex justify-end gap-2">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setEditing(true)}
          aria-label="Edit merchant"
        >
          <Pencil className="h-4 w-4" />
          <span className="sr-only">Edit merchant</span>
        </Button>
        <Button
          variant="ghost"
          size="icon"
          disabled={archiveMutation.isPending}
          onClick={() => archiveMutation.mutate()}
          aria-label={
            merchant.archivedAt ? "Restore merchant" : "Archive merchant"
          }
        >
          {merchant.archivedAt ? (
            <ArchiveRestore className="h-4 w-4" />
          ) : (
            <Archive className="h-4 w-4" />
          )}
          <span className="sr-only">
            {merchant.archivedAt ? "Restore merchant" : "Archive merchant"}
          </span>
        </Button>
      </div>
    </li>
  );
}

function MerchantAvatar({ logoUrl }: { logoUrl?: string | null }) {
  if (logoUrl) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={logoUrl}
        alt=""
        className="h-6 w-6 shrink-0 rounded border border-border bg-surface object-cover"
      />
    );
  }
  return (
    <div
      aria-hidden
      className="h-6 w-6 shrink-0 rounded border border-border bg-surface-subtle"
    />
  );
}

export function MerchantForm({
  slug,
  workspaceId,
  leafCategories,
  merchant,
  transactionCount,
  onDone,
  onCancel,
}: {
  /** Workspace slug — used to deep-link to an existing merchant when a
   *  rename collides with another active merchant in this workspace. */
  slug: string;
  workspaceId: string;
  leafCategories: Category[];
  merchant?: Merchant;
  /** How many transactions this merchant has. Used to decide whether to
   *  prompt the cascade dialog when the user changes `defaultCategoryId`.
   *  Pass 0 (the default) to never prompt — appropriate when the count is
   *  unknown in the calling view. */
  transactionCount?: number;
  onDone: () => void;
  onCancel: () => void;
}) {
  const queryClient = useQueryClient();
  const [canonicalName, setCanonicalName] = React.useState(
    merchant?.canonicalName ?? ""
  );
  const [defaultCategoryId, setDefaultCategoryId] = React.useState(
    merchant?.defaultCategoryId ?? ""
  );
  const [website, setWebsite] = React.useState(merchant?.website ?? "");
  const [industry, setIndustry] = React.useState(merchant?.industry ?? "");
  const [logoUrl, setLogoUrl] = React.useState(merchant?.logoUrl ?? "");
  const [notes, setNotes] = React.useState(merchant?.notes ?? "");
  const [cascadeDialogOpen, setCascadeDialogOpen] = React.useState(false);

  const idSuffix = merchant?.id ?? "new";
  const knownTransactionCount = transactionCount ?? 0;

  const categoryNameById = React.useMemo(() => {
    const map = new Map<string, string>();
    for (const category of leafCategories) map.set(category.id, category.name);
    return map;
  }, [leafCategories]);

  const mutation = useMutation<MerchantPatchResult | Merchant, Error, boolean>({
    mutationFn: async (cascade: boolean) => {
      const trimmedName = canonicalName.trim();
      const normalizedCategory = defaultCategoryId || null;
      const normalizedWebsite = website.trim() || null;
      const normalizedIndustry = industry.trim() || null;
      const normalizedLogoUrl = logoUrl.trim() || null;
      const normalizedNotes = notes.trim() || null;

      if (merchant) {
        return updateMerchant(workspaceId, merchant.id, {
          canonicalName: trimmedName,
          defaultCategoryId: normalizedCategory,
          website: normalizedWebsite,
          industry: normalizedIndustry,
          logoUrl: normalizedLogoUrl,
          notes: normalizedNotes,
          cascade,
        });
      }
      return createMerchant(workspaceId, {
        canonicalName: trimmedName,
        defaultCategoryId: normalizedCategory,
        website: normalizedWebsite,
        industry: normalizedIndustry,
        logoUrl: normalizedLogoUrl,
        notes: normalizedNotes,
      });
    },
    onSuccess: async (_data, cascade) => {
      setCascadeDialogOpen(false);
      await queryClient.invalidateQueries({
        queryKey: ["merchants", workspaceId],
      });
      if (merchant) {
        await queryClient.invalidateQueries({
          queryKey: ["merchant", workspaceId, merchant.id],
        });
        if (cascade) {
          // The cascade-true path may have re-categorised transactions on
          // the server; refresh any transaction queries so the new category
          // is reflected immediately.
          await queryClient.invalidateQueries({
            queryKey: ["transactions", workspaceId],
          });
        }
      }
      onDone();
    },
  });

  const apiError =
    mutation.error instanceof ApiError ? mutation.error : null;
  const conflictExistingMerchantId =
    apiError?.body?.code === "merchant_name_conflict" &&
    typeof (apiError.body.details as { existingMerchantId?: unknown })
      ?.existingMerchantId === "string"
      ? ((apiError.body.details as { existingMerchantId: string })
          .existingMerchantId as string)
      : null;

  const cascadeResult: MerchantPatchResult | null =
    merchant &&
    mutation.data &&
    typeof mutation.data === "object" &&
    "merchant" in mutation.data
      ? (mutation.data as MerchantPatchResult)
      : null;
  const cascadedCount = cascadeResult?.cascadedTransactionCount ?? 0;

  const oldCategoryName = merchant?.defaultCategoryId
    ? (categoryNameById.get(merchant.defaultCategoryId) ?? null)
    : null;
  const newCategoryName = defaultCategoryId
    ? (categoryNameById.get(defaultCategoryId) ?? null)
    : null;

  const submitForm = () => {
    if (!canonicalName.trim()) return;
    const isEdit = !!merchant;
    if (!isEdit) {
      mutation.mutate(false);
      return;
    }
    const defaultChanged =
      (merchant?.defaultCategoryId ?? "") !== (defaultCategoryId ?? "");
    if (!defaultChanged) {
      mutation.mutate(false);
      return;
    }
    if (knownTransactionCount <= 0) {
      // No existing transactions — cascade is a no-op, so don't prompt.
      mutation.mutate(false);
      return;
    }
    setCascadeDialogOpen(true);
  };

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        submitForm();
      }}
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Field label="Canonical name" htmlFor={`merchant-name-${idSuffix}`}>
          <Input
            id={`merchant-name-${idSuffix}`}
            value={canonicalName}
            onChange={(event) => setCanonicalName(event.target.value)}
            placeholder="Whole Foods Market"
            required
          />
        </Field>
        <Field
          label="Default category"
          htmlFor={`merchant-category-${idSuffix}`}
          hint="Used as the default when classifying new transactions for this merchant."
        >
          <Select
            id={`merchant-category-${idSuffix}`}
            value={defaultCategoryId}
            onChange={(event) => setDefaultCategoryId(event.target.value)}
          >
            <option value="">— No default —</option>
            {leafCategories.map((category) => (
              <option key={category.id} value={category.id}>
                {category.name}
                {category.archivedAt ? " (archived)" : ""}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Website" htmlFor={`merchant-website-${idSuffix}`}>
          <Input
            id={`merchant-website-${idSuffix}`}
            value={website}
            onChange={(event) => setWebsite(event.target.value)}
            placeholder="https://wholefoodsmarket.com"
          />
        </Field>
        <Field label="Industry" htmlFor={`merchant-industry-${idSuffix}`}>
          <Input
            id={`merchant-industry-${idSuffix}`}
            value={industry}
            onChange={(event) => setIndustry(event.target.value)}
            placeholder="Grocery"
          />
        </Field>
        <Field
          label="Logo URL"
          htmlFor={`merchant-logo-${idSuffix}`}
          className="md:col-span-2"
        >
          <Input
            id={`merchant-logo-${idSuffix}`}
            value={logoUrl}
            onChange={(event) => setLogoUrl(event.target.value)}
            placeholder="https://logo.example/whole-foods.png"
          />
        </Field>
        <Field
          label="Notes"
          htmlFor={`merchant-notes-${idSuffix}`}
          className="md:col-span-2"
        >
          <Textarea
            id={`merchant-notes-${idSuffix}`}
            value={notes}
            onChange={(event) => setNotes(event.target.value)}
            placeholder="Internal notes — not shown elsewhere."
            rows={3}
          />
        </Field>
      </div>

      {conflictExistingMerchantId ? (
        <FormError>
          Another active merchant in this workspace already uses that name.{" "}
          <Link
            href={
              `/w/${slug}/merchants/${conflictExistingMerchantId}` as Route
            }
            className="underline"
          >
            Open the existing merchant
          </Link>
          {" "}— from there you can Merge this one into it.
        </FormError>
      ) : apiError ? (
        <FormError>{apiError.message}</FormError>
      ) : null}

      {cascadedCount > 0 ? (
        <div className="rounded-[8px] border border-border bg-surface-subtle px-3 py-2 text-[12px] text-fg-muted">
          Re-categorised{" "}
          <span className="font-medium text-fg tabular-nums">
            {cascadedCount}
          </span>{" "}
          existing transaction{cascadedCount === 1 ? "" : "s"}.
        </div>
      ) : null}

      <div className="flex items-center justify-end gap-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          <X className="h-4 w-4" />
          Cancel
        </Button>
        <Button
          type="submit"
          disabled={mutation.isPending || !canonicalName.trim()}
        >
          <Check className="h-4 w-4" />
          {merchant ? "Save changes" : "Create merchant"}
        </Button>
      </div>

      {merchant ? (
        <MerchantDefaultCategoryDialog
          open={cascadeDialogOpen}
          merchantName={merchant.canonicalName}
          oldCategoryName={oldCategoryName}
          newCategoryName={newCategoryName}
          busy={mutation.isPending}
          onApply={() => mutation.mutate(true)}
          onSkip={() => mutation.mutate(false)}
          onCancel={() => {
            if (mutation.isPending) return;
            setCascadeDialogOpen(false);
          }}
        />
      ) : null}
    </form>
  );
}


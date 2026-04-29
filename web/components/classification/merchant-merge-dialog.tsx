"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { FormError } from "@/components/ui/form-error";
import {
  ApiError,
  fetchMerchants,
  mergeMerchants,
  previewMergeMerchants,
  type Merchant,
  type MergePreview,
} from "@/lib/api/client";

export type MerchantMergeDialogProps = {
  open: boolean;
  workspaceId: string;
  workspaceSlug: string;
  source: Merchant;
  onClose: () => void;
};

export function MerchantMergeDialog(props: MerchantMergeDialogProps) {
  if (!props.open) return null;
  // Remount the inner body each time the dialog opens so internal form state
  // (search, target selection, cascade checkbox) resets without a setState
  // inside an effect. The `source.id` part keeps state isolated per merchant
  // when the parent swaps source while the dialog is open.
  return <MerchantMergeDialogBody key={props.source.id} {...props} />;
}

function MerchantMergeDialogBody({
  workspaceId,
  workspaceSlug,
  source,
  onClose,
}: MerchantMergeDialogProps) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const cardRef = React.useRef<HTMLDivElement | null>(null);
  const searchInputRef = React.useRef<HTMLInputElement | null>(null);

  const [search, setSearch] = React.useState("");
  const [targetId, setTargetId] = React.useState<string | null>(null);
  // Default checked when applicable; the request still gates on
  // cascadedCountIfApplied > 0 so this is harmless when no cascade is offered.
  const [applyDefault, setApplyDefault] = React.useState(true);

  const merchantsQuery = useQuery({
    queryKey: ["merchants", workspaceId, false],
    queryFn: () => fetchMerchants(workspaceId, { includeArchived: false }),
  });

  const previewQuery = useQuery<MergePreview>({
    queryKey: ["merchant-merge-preview", workspaceId, source.id, targetId],
    queryFn: () =>
      previewMergeMerchants(workspaceId, source.id, { targetId: targetId! }),
    enabled: !!targetId,
  });

  const mergeMutation = useMutation({
    mutationFn: () =>
      mergeMerchants(workspaceId, source.id, {
        targetId: targetId!,
        applyTargetDefault:
          applyDefault &&
          (previewQuery.data?.cascadedCountIfApplied ?? 0) > 0,
      }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["merchants", workspaceId] }),
        queryClient.invalidateQueries({
          queryKey: ["merchant-aliases", workspaceId],
        }),
        queryClient.invalidateQueries({
          queryKey: ["transactions", workspaceId],
        }),
      ]);
      router.push(
        `/w/${workspaceSlug}/merchants/${targetId}` as Route
      );
      onClose();
    },
  });

  // Esc-to-close + initial focus + lock background scroll, mirroring the
  // cascade dialog. Esc is suppressed while a merge mutation is pending.
  React.useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        if (mergeMutation.isPending) return;
        event.stopPropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", handleKey);
    // Move focus into the search field so keyboard users can start typing.
    searchInputRef.current?.focus();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", handleKey);
      document.body.style.overflow = previousOverflow;
    };
  }, [onClose, mergeMutation.isPending]);

  const candidates = React.useMemo(() => {
    const all = merchantsQuery.data ?? [];
    const term = search.trim().toLowerCase();
    return all
      .filter((m) => m.id !== source.id)
      // includeArchived=false already filters server-side, but be defensive.
      .filter((m) => !m.archivedAt)
      .filter((m) =>
        term.length === 0
          ? true
          : m.canonicalName.toLowerCase().includes(term)
      )
      .sort((a, b) => a.canonicalName.localeCompare(b.canonicalName));
  }, [merchantsQuery.data, search, source.id]);

  const selectedTarget = React.useMemo(
    () =>
      (merchantsQuery.data ?? []).find((m) => m.id === targetId) ?? null,
    [merchantsQuery.data, targetId]
  );

  const preview = previewQuery.data ?? null;
  const cascadeAvailable = (preview?.cascadedCountIfApplied ?? 0) > 0;

  const mergeError =
    mergeMutation.error instanceof ApiError
      ? mergeMutation.error.message
      : mergeMutation.error
        ? "Couldn't merge merchants. Please try again."
        : null;

  const previewError =
    previewQuery.error instanceof ApiError
      ? previewQuery.error.message
      : previewQuery.error
        ? "Couldn't load merge preview. Please try again."
        : null;

  const merchantsError =
    merchantsQuery.error instanceof ApiError
      ? merchantsQuery.error.message
      : merchantsQuery.error
        ? "Couldn't load merchants. Please try again."
        : null;

  const confirmDisabled =
    !targetId ||
    !preview ||
    previewQuery.isLoading ||
    previewQuery.isFetching ||
    mergeMutation.isPending;

  return (
    <div
      role="presentation"
      className="fixed inset-0 z-50 flex items-center justify-center bg-fg/30 px-4 py-8"
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        if (mergeMutation.isPending) return;
        onClose();
      }}
    >
      <div
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="merchant-merge-title"
        tabIndex={-1}
        className="flex w-full max-w-lg flex-col gap-4 rounded-[16px] border border-border bg-surface p-5 outline-none focus-visible:ring-2 focus-visible:ring-accent"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="flex flex-col gap-1">
          <h2
            id="merchant-merge-title"
            className="text-[15px] font-medium tracking-tight text-fg"
          >
            Merge{" "}
            <span className="font-semibold">{source.canonicalName}</span> into…
          </h2>
          <p className="text-[12px] text-fg-muted">
            Pick a target merchant. {source.canonicalName} will be deleted and
            its transactions, aliases, and metadata will move onto the target.
          </p>
        </div>

        {selectedTarget === null ? (
          <Field label="Target merchant" htmlFor="merchant-merge-search">
            <Input
              ref={searchInputRef}
              id="merchant-merge-search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search merchants…"
              autoComplete="off"
            />
          </Field>
        ) : (
          <div className="flex items-center justify-between gap-3 rounded-[8px] border border-border bg-surface-subtle px-3 py-2">
            <div className="min-w-0">
              <div className="text-[11px] font-medium tracking-[0.07em] text-fg-faint uppercase">
                Target
              </div>
              <div className="truncate text-[13px] text-fg">
                {selectedTarget.canonicalName}
              </div>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setTargetId(null);
                // Refocus search so the user can pick another target.
                requestAnimationFrame(() => searchInputRef.current?.focus());
              }}
              disabled={mergeMutation.isPending}
            >
              Change
            </Button>
          </div>
        )}

        {selectedTarget === null ? (
          <div className="max-h-64 overflow-y-auto rounded-[8px] border border-border">
            {merchantsQuery.isLoading ? (
              <div className="px-3 py-3 text-[12px] text-fg-muted">
                Loading merchants…
              </div>
            ) : merchantsError ? (
              <div className="px-3 py-3 text-[12px] text-danger">
                {merchantsError}
              </div>
            ) : candidates.length === 0 ? (
              <div className="px-3 py-3 text-[12px] text-fg-muted">
                {search.trim().length === 0
                  ? "No other merchants in this workspace."
                  : "No merchants match. You can rename this merchant instead."}
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {candidates.slice(0, 50).map((m) => (
                  <li key={m.id}>
                    <button
                      type="button"
                      className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-[13px] text-fg hover:bg-surface-subtle focus-visible:bg-surface-subtle focus-visible:outline-none"
                      onClick={() => setTargetId(m.id)}
                    >
                      <span className="truncate">{m.canonicalName}</span>
                      {m.industry ? (
                        <span className="shrink-0 text-[12px] text-fg-faint">
                          {m.industry}
                        </span>
                      ) : null}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        ) : (
          <MergePreviewSection
            preview={preview}
            isLoading={previewQuery.isLoading || previewQuery.isFetching}
            error={previewError}
            cascadeAvailable={cascadeAvailable}
            applyDefault={applyDefault}
            onApplyDefaultChange={setApplyDefault}
            disabled={mergeMutation.isPending}
          />
        )}

        {mergeError ? <FormError>{mergeError}</FormError> : null}

        <div className="mt-1 flex flex-col-reverse items-stretch gap-2 sm:flex-row sm:items-center sm:justify-end">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={onClose}
            disabled={mergeMutation.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
            size="sm"
            onClick={() => mergeMutation.mutate()}
            disabled={confirmDisabled}
          >
            {mergeMutation.isPending ? "Merging…" : "Confirm merge"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function MergePreviewSection({
  preview,
  isLoading,
  error,
  cascadeAvailable,
  applyDefault,
  onApplyDefaultChange,
  disabled,
}: {
  preview: MergePreview | null;
  isLoading: boolean;
  error: string | null;
  cascadeAvailable: boolean;
  applyDefault: boolean;
  onApplyDefaultChange: (next: boolean) => void;
  disabled: boolean;
}) {
  if (error) {
    return (
      <div className="rounded-[8px] border border-border bg-surface-subtle px-3 py-3 text-[12px] text-danger">
        {error}
      </div>
    );
  }

  if (isLoading || !preview) {
    return (
      <div className="rounded-[8px] border border-border bg-surface-subtle px-3 py-3 text-[12px] text-fg-muted">
        Loading preview…
      </div>
    );
  }

  const targetName = preview.targetCanonicalName;
  const blanks = preview.blankFillFields;

  return (
    <div className="flex flex-col gap-3 rounded-[8px] border border-border bg-surface-subtle px-3 py-3 text-[13px] text-fg">
      <ul className="flex flex-col gap-1.5">
        <li className="flex items-baseline gap-2">
          <span className="text-fg-faint">•</span>
          <span>
            Move{" "}
            <span className="font-medium tabular-nums">
              {preview.movedCount}
            </span>{" "}
            transaction{preview.movedCount === 1 ? "" : "s"} to{" "}
            <span className="font-medium">{targetName}</span>.
          </span>
        </li>
        <li className="flex items-baseline gap-2">
          <span className="text-fg-faint">•</span>
          <span>
            Capture{" "}
            <span className="font-medium tabular-nums">
              {preview.capturedAliasCount}
            </span>{" "}
            alias{preview.capturedAliasCount === 1 ? "" : "es"} on{" "}
            <span className="font-medium">{targetName}</span>{" "}
            <span className="text-fg-muted">
              (source canonical name and any aliases not already on the target).
            </span>
          </span>
        </li>
        {blanks.length > 0 ? (
          <li className="flex items-baseline gap-2">
            <span className="text-fg-faint">•</span>
            <span>
              Fill blank fields on{" "}
              <span className="font-medium">{targetName}</span>:{" "}
              <span className="text-fg-muted">{blanks.join(", ")}</span>.
            </span>
          </li>
        ) : null}
      </ul>

      {cascadeAvailable ? (
        <label className="flex items-start gap-2 rounded-[8px] border border-border bg-surface px-3 py-2 text-[12px] text-fg-muted">
          <input
            type="checkbox"
            className="mt-0.5 h-3.5 w-3.5 accent-accent"
            checked={applyDefault}
            onChange={(event) => onApplyDefaultChange(event.target.checked)}
            disabled={disabled}
          />
          <span>
            <span className="font-medium tabular-nums text-fg">
              {preview.cascadedCountIfApplied}
            </span>{" "}
            of these transactions currently match{" "}
            <span className="font-medium text-fg">
              {preview.sourceCanonicalName}
            </span>
            ’s default category. Re-categorise to{" "}
            <span className="font-medium text-fg">{targetName}</span>’s default?
          </span>
        </label>
      ) : null}
    </div>
  );
}

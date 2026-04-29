"use client";

import * as React from "react";
import { Button } from "@/components/ui/button";

export type MerchantDefaultCategoryDialogProps = {
  open: boolean;
  merchantName: string;
  /** null when the merchant had no default category before. */
  oldCategoryName: string | null;
  /** null when the user is clearing the default category. */
  newCategoryName: string | null;
  /** Triggered when the user picks "Apply to existing & future". */
  onApply: () => void;
  /** Triggered when the user picks "Only future transactions". */
  onSkip: () => void;
  /** Triggered when the user dismisses the dialog (Esc / backdrop click /
   *  cancel button) without choosing an option. */
  onCancel: () => void;
  /** Disables the action buttons (e.g. while a mutation is pending). */
  busy?: boolean;
};

const NONE_LABEL = "no category";

export function MerchantDefaultCategoryDialog({
  open,
  merchantName,
  oldCategoryName,
  newCategoryName,
  onApply,
  onSkip,
  onCancel,
  busy = false,
}: MerchantDefaultCategoryDialogProps) {
  const cardRef = React.useRef<HTMLDivElement | null>(null);

  // Esc-to-close + initial focus management.
  React.useEffect(() => {
    if (!open) return;
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        onCancel();
      }
    };
    document.addEventListener("keydown", handleKey);
    // Move focus into the dialog so keyboard users land here.
    cardRef.current?.focus();
    // Lock background scroll while open.
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", handleKey);
      document.body.style.overflow = previousOverflow;
    };
  }, [open, onCancel]);

  if (!open) return null;

  const oldLabel = oldCategoryName ?? NONE_LABEL;
  const newLabel = newCategoryName ?? NONE_LABEL;

  let body: React.ReactNode;
  if (oldCategoryName === null && newCategoryName !== null) {
    body = (
      <>
        <strong className="font-medium text-fg">{merchantName}</strong> has no
        default category yet. You&rsquo;re setting it to{" "}
        <strong className="font-medium text-fg">{newCategoryName}</strong>. If
        you continue, every transaction of {merchantName}&rsquo;s that currently
        has no category will be re-categorised to{" "}
        <strong className="font-medium text-fg">{newCategoryName}</strong>.
        Manually-categorised transactions won&rsquo;t be touched.
      </>
    );
  } else if (newCategoryName === null && oldCategoryName !== null) {
    body = (
      <>
        <strong className="font-medium text-fg">{merchantName}</strong>&rsquo;s
        default category is being cleared. If you continue, every transaction of{" "}
        {merchantName}&rsquo;s whose category is currently{" "}
        <strong className="font-medium text-fg">{oldCategoryName}</strong> will
        be reset to no category. Manually-categorised transactions (those with a
        different category) won&rsquo;t be touched.
      </>
    );
  } else {
    body = (
      <>
        <strong className="font-medium text-fg">{merchantName}</strong>&rsquo;s
        default category is changing from{" "}
        <strong className="font-medium text-fg">{oldLabel}</strong> to{" "}
        <strong className="font-medium text-fg">{newLabel}</strong>. If you
        continue, every transaction of {merchantName}&rsquo;s whose category is
        currently <strong className="font-medium text-fg">{oldLabel}</strong>{" "}
        will be re-categorised to{" "}
        <strong className="font-medium text-fg">{newLabel}</strong>.
        Manually-categorised transactions (those with a different category)
        won&rsquo;t be touched.
      </>
    );
  }

  return (
    <div
      role="presentation"
      className="fixed inset-0 z-50 flex items-center justify-center bg-fg/30 px-4 py-8"
      onClick={(event) => {
        // Backdrop click.
        if (event.target === event.currentTarget) onCancel();
      }}
    >
      <div
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="merchant-default-cascade-title"
        aria-describedby="merchant-default-cascade-body"
        tabIndex={-1}
        className="w-full max-w-md rounded-[16px] border border-border bg-surface p-5 outline-none focus-visible:ring-2 focus-visible:ring-accent"
        onClick={(event) => event.stopPropagation()}
      >
        <h2
          id="merchant-default-cascade-title"
          className="text-[15px] font-medium tracking-tight text-fg"
        >
          Apply new default category to existing transactions?
        </h2>
        <p
          id="merchant-default-cascade-body"
          className="mt-3 text-[13px] leading-relaxed text-fg-muted"
        >
          {body}
        </p>
        <div className="mt-5 flex flex-col-reverse items-stretch gap-2 sm:flex-row sm:items-center sm:justify-end">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={onSkip}
            disabled={busy}
          >
            Only future transactions
          </Button>
          <Button
            type="button"
            variant="primary"
            size="sm"
            onClick={onApply}
            disabled={busy}
          >
            Apply to existing & future
          </Button>
        </div>
      </div>
    </div>
  );
}

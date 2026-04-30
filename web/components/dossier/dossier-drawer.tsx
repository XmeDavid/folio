"use client";

import * as React from "react";
import { X } from "lucide-react";

export function DossierDrawer({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = previousOverflow;
    };
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-40">
      <div
        className="bg-fg/30 absolute inset-0"
        onClick={onClose}
        aria-hidden
      />
      <aside
        className="bg-surface border-border absolute inset-y-0 right-0 w-[420px] max-w-full overflow-y-auto border-l"
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <header className="bg-surface border-border sticky top-0 z-10 flex items-center justify-between border-b px-4 py-3">
          <h2 className="text-fg text-[14px] font-semibold">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            className="text-fg-muted hover:text-fg"
            aria-label="Close drawer"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="p-4">{children}</div>
      </aside>
    </div>
  );
}

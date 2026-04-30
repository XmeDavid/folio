"use client";

import * as React from "react";
import type { DossierTabSpec } from "./registry";

/**
 * Right-edge floating tab. Collapsed by default — only the icon + count
 * badge are visible. On hover (or keyboard focus), the label slides out
 * from the edge. Click invokes onClick (the container opens the drawer).
 */
export function DossierTab({
  spec,
  onClick,
}: {
  spec: DossierTabSpec;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={`${spec.label} (${spec.count} pending)`}
      title={spec.label}
      className="
        group bg-surface border-border text-fg flex items-center gap-2
        rounded-l-md border border-r-0 px-2.5 py-2
        transition-transform hover:-translate-x-0.5
        focus:outline-none focus-visible:ring-2 focus-visible:ring-accent
      "
    >
      {spec.icon}
      <span
        className="
          overflow-hidden whitespace-nowrap text-[12px] font-medium
          max-w-0 group-hover:max-w-[200px] group-focus-visible:max-w-[200px]
          transition-[max-width] duration-200 ease-out
        "
      >
        {spec.label}
      </span>
      <span className="bg-accent rounded px-1.5 py-0.5 text-[11px] font-semibold text-white">
        {spec.count}
      </span>
    </button>
  );
}

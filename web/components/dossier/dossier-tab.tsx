"use client";

import * as React from "react";
import type { DossierTabSpec } from "./registry";

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
      className="bg-surface border-border text-fg flex items-center gap-2 rounded-l-md border border-r-0 px-3 py-2 transition-transform hover:-translate-x-1 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      {spec.icon}
      <span className="text-[12px] font-medium">{spec.label}</span>
      <span className="bg-accent rounded px-1.5 py-0.5 text-[11px] font-semibold text-white">
        {spec.count}
      </span>
    </button>
  );
}

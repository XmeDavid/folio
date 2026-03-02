"use client";

import { cn } from "@/lib/utils";

const currencies = ["CHF", "USD", "EUR"] as const;
export type DisplayCurrency = (typeof currencies)[number];

export function CurrencyToggle({
  value,
  onChange,
}: {
  value: DisplayCurrency;
  onChange: (c: DisplayCurrency) => void;
}) {
  return (
    <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
      {currencies.map((c) => (
        <button
          key={c}
          onClick={() => onChange(c)}
          className={cn(
            "px-3 py-1.5 text-xs font-mono font-medium rounded-md transition-all",
            value === c
              ? "bg-accent text-bg-primary shadow-sm"
              : "text-text-tertiary hover:text-text-secondary"
          )}
        >
          {c}
        </button>
      ))}
    </div>
  );
}

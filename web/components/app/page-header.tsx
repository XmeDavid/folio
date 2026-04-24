import * as React from "react";
import { cn } from "@/lib/utils";

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  className,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 border-b border-[--color-border] pb-6",
        className
      )}
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between sm:gap-6">
        <div className="flex min-w-0 flex-col gap-1">
          {eyebrow ? (
            <div className="text-[11px] font-medium tracking-[0.07em] text-[--color-fg-faint] uppercase">
              {eyebrow}
            </div>
          ) : null}
          <h1 className="text-[28px] leading-tight font-normal tracking-tight text-[--color-fg]">
            {title}
          </h1>
          {description ? (
            <p className="max-w-2xl text-[14px] text-[--color-fg-muted]">
              {description}
            </p>
          ) : null}
        </div>
        {actions ? (
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        ) : null}
      </div>
    </div>
  );
}

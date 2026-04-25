import * as React from "react";
import { cn } from "@/lib/utils";

export function EmptyState({
  title,
  description,
  action,
  className,
}: {
  title: string;
  description?: string;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-2 rounded-[12px] border border-dashed border-border bg-surface px-6 py-12 text-center",
        className
      )}
    >
      <p className="text-[15px] font-medium text-fg">{title}</p>
      {description ? (
        <p className="max-w-md text-[13px] text-fg-muted">
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}

export function ErrorBanner({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="rounded-[12px] border border-border bg-[#FAECE7] px-4 py-3 text-[13px] text-[#7A3B20]">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 h-2 w-2 rounded-full bg-accent" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="font-medium">{title}</div>
          {description ? (
            <div className="text-[12px] text-[#7A3B20]/80">{description}</div>
          ) : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </div>
    </div>
  );
}

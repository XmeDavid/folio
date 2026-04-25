import * as React from "react";
import { cn } from "@/lib/utils";

export function InfoPanel({
  title,
  action,
  children,
  className,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section
      className={cn(
        "rounded-[12px] border border-border bg-surface p-4",
        className
      )}
    >
      <header className="mb-3 flex items-center justify-between gap-2">
        <h2 className="text-[15px] font-medium tracking-tight">{title}</h2>
        {action ? <div className="shrink-0">{action}</div> : null}
      </header>
      <div className="flex flex-col gap-2">{children}</div>
    </section>
  );
}

export function MetaItem({
  label,
  value,
  className,
}: {
  label: string;
  value?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("border-b border-border pb-3", className)}>
      <dt className="text-[12px] text-fg-faint">{label}</dt>
      <dd className="mt-1 break-all text-[14px] text-fg">{value ?? "None"}</dd>
    </div>
  );
}

export function MetaRow({
  label,
  value,
  className,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex justify-between gap-4 border-t border-border pt-2 text-[14px]",
        "first:border-t-0 first:pt-0",
        className
      )}
    >
      <span className="text-fg-muted">{label}</span>
      <span className="text-right text-fg">{value}</span>
    </div>
  );
}

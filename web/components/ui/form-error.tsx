import * as React from "react";
import { cn } from "@/lib/utils";

export function FormError({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  if (!children) return null;
  return (
    <div
      role="alert"
      className={cn(
        "rounded-[8px] border border-border bg-[#F5DADA] px-3 py-2 text-[13px] text-danger",
        className
      )}
    >
      {children}
    </div>
  );
}

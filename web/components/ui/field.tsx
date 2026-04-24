import * as React from "react";
import { Label } from "./label";
import { cn } from "@/lib/utils";

export function Field({
  label,
  hint,
  error,
  htmlFor,
  children,
  className,
}: {
  label: string;
  hint?: string;
  error?: string;
  htmlFor?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {error ? (
        <p className="text-[12px] text-[--color-danger]">{error}</p>
      ) : hint ? (
        <p className="text-[12px] text-[--color-fg-faint]">{hint}</p>
      ) : null}
    </div>
  );
}

import * as React from "react";
import { cn } from "@/lib/utils";

export type InputProps = React.InputHTMLAttributes<HTMLInputElement>;

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type = "text", ...props }, ref) => (
    <input
      ref={ref}
      type={type}
      className={cn(
        "h-9 w-full rounded-[8px] border border-border bg-surface px-3 text-[14px] text-fg",
        "placeholder:text-fg-faint",
        "transition-colors duration-150 focus:border-border-strong focus:ring-2 focus:ring-accent focus:ring-offset-0 focus:outline-none",
        "disabled:cursor-not-allowed disabled:opacity-60",
        className
      )}
      {...props}
    />
  )
);
Input.displayName = "Input";

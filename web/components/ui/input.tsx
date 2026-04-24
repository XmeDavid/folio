import * as React from "react";
import { cn } from "@/lib/utils";

export type InputProps = React.InputHTMLAttributes<HTMLInputElement>;

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type = "text", ...props }, ref) => (
    <input
      ref={ref}
      type={type}
      className={cn(
        "h-9 w-full rounded-[8px] border border-[--color-border] bg-[--color-surface] px-3 text-[14px] text-[--color-fg]",
        "placeholder:text-[--color-fg-faint]",
        "transition-colors duration-150 focus:border-[--color-border-strong] focus:ring-2 focus:ring-[--color-accent] focus:ring-offset-0 focus:outline-none",
        "disabled:cursor-not-allowed disabled:opacity-60",
        className
      )}
      {...props}
    />
  )
);
Input.displayName = "Input";

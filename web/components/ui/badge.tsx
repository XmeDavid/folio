import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badge = cva(
  "inline-flex items-center rounded-[999px] px-2.5 py-0.5 text-[11px] font-medium",
  {
    variants: {
      variant: {
        neutral: "bg-surface-subtle text-fg-muted",
        accent: "bg-accent-tint text-accent",
        amber: "bg-[#F7ECD9] text-amber",
        success: "bg-[#E5EFDB] text-success",
        danger: "bg-[#F5DADA] text-danger",
        info: "bg-[#DBE7F4] text-info",
      },
    },
    defaultVariants: { variant: "neutral" },
  }
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>, VariantProps<typeof badge> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badge({ variant }), className)} {...props} />;
}

"use client";

import { useState, useRef, useCallback } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/utils";
import { Info } from "lucide-react";

export function Tip({
  text,
  children,
  className,
}: {
  text: string;
  children?: React.ReactNode;
  className?: string;
}) {
  const [pos, setPos] = useState<{ x: number; y: number; above: boolean } | null>(null);
  const iconRef = useRef<SVGSVGElement>(null);

  const show = useCallback(() => {
    if (!iconRef.current) return;
    const rect = iconRef.current.getBoundingClientRect();
    const above = rect.top > 80;
    setPos({
      x: rect.left + rect.width / 2,
      y: above ? rect.top : rect.bottom,
      above,
    });
  }, []);

  const hide = useCallback(() => setPos(null), []);

  return (
    <span
      className={cn("inline-flex items-center gap-1", className)}
      onMouseEnter={show}
      onMouseLeave={hide}
    >
      {children}
      <Info
        ref={iconRef}
        size={11}
        className={cn(
          "shrink-0 transition-colors cursor-help",
          pos ? "text-text-secondary" : "text-text-tertiary/50"
        )}
      />
      {pos &&
        typeof document !== "undefined" &&
        createPortal(
          <span
            role="tooltip"
            style={{
              position: "fixed",
              left: pos.x,
              top: pos.above ? pos.y : pos.y + 6,
              transform: pos.above
                ? "translate(-50%, -100%) translateY(-6px)"
                : "translate(-50%, 0)",
            }}
            className="pointer-events-none px-2.5 py-1.5 rounded-lg bg-bg-tertiary border border-border-subtle shadow-lg text-[11px] leading-snug text-text-secondary font-sans font-normal normal-case tracking-normal whitespace-normal max-w-[240px] w-max z-[9999]"
          >
            {text}
          </span>,
          document.body
        )}
    </span>
  );
}

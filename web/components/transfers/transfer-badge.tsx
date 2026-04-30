"use client";

import { ArrowRightLeft, ArrowUpRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";

/**
 * Visual marker for a transaction that participates in a transfer pair.
 *
 * `external = true` → outbound-to-external (no linked counterpart in any
 * tracked account). `external = false` (default) → both legs live in this
 * workspace and the counterpart is reachable via `transferCounterpartId`.
 */
export function TransferBadge({ external = false }: { external?: boolean }) {
  if (external) {
    return (
      <Badge variant="neutral" className="gap-1">
        <ArrowUpRight className="h-3 w-3" />
        External
      </Badge>
    );
  }
  return (
    <Badge variant="info" className="gap-1">
      <ArrowRightLeft className="h-3 w-3" />
      Transfer
    </Badge>
  );
}

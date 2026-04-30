"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRightLeft } from "lucide-react";
import { fetchPendingTransferCandidateCount } from "@/lib/api/client";
import { useRegisterDossierTab } from "@/components/dossier/registry";
import { TransfersReviewQueue } from "./transfers-review-queue";

export function TransfersReviewTab({ workspaceId }: { workspaceId: string }) {
  const countQuery = useQuery({
    queryKey: ["transfer-candidate-count", workspaceId],
    queryFn: () => fetchPendingTransferCandidateCount(workspaceId),
    refetchInterval: 30_000,
    enabled: !!workspaceId,
  });
  const count = countQuery.data?.count ?? 0;

  const spec = React.useMemo(
    () =>
      count > 0
        ? {
            id: "transfers-review",
            label: "Review transfers",
            icon: <ArrowRightLeft className="h-3.5 w-3.5" />,
            count,
            drawerContent: (
              <TransfersReviewQueue workspaceId={workspaceId} mode="drawer" />
            ),
          }
        : null,
    [count, workspaceId],
  );
  useRegisterDossierTab(spec);
  return null;
}

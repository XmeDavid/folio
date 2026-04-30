"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  declineTransferCandidate,
  fetchPendingTransferCandidates,
  fetchTransaction,
  manualPairTransfer,
  type TransferCandidate,
  type Transaction,
} from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { LoadingText, EmptyState } from "@/components/app/empty";

export function TransfersReviewQueue({
  workspaceId,
  mode = "drawer",
}: {
  workspaceId: string;
  mode?: "drawer" | "page";
}) {
  const queryClient = useQueryClient();
  const candidatesQuery = useQuery({
    queryKey: ["transfer-candidates", workspaceId],
    queryFn: () => fetchPendingTransferCandidates(workspaceId),
    enabled: !!workspaceId,
  });

  if (candidatesQuery.isLoading) return <LoadingText />;
  const candidates = candidatesQuery.data ?? [];
  if (candidates.length === 0)
    return (
      <EmptyState
        title="No pending suggestions"
        description="Folio surfaces possible transfers here after each import."
      />
    );

  return (
    <div className="flex flex-col gap-3">
      {candidates.map((c) => (
        <CandidateRow
          key={c.id}
          workspaceId={workspaceId}
          candidate={c}
          onDone={() =>
            queryClient.invalidateQueries({
              queryKey: ["transfer-candidates", workspaceId],
            })
          }
          mode={mode}
        />
      ))}
    </div>
  );
}

function CandidateRow({
  workspaceId,
  candidate,
  onDone,
  mode,
}: {
  workspaceId: string;
  candidate: TransferCandidate;
  onDone: () => void;
  mode: "drawer" | "page";
}) {
  const [chosenDestId, setChosenDestId] = React.useState<string | null>(null);
  const queryClient = useQueryClient();

  const sourceQuery = useQuery({
    queryKey: ["transaction", workspaceId, candidate.sourceTransactionId],
    queryFn: () => fetchTransaction(workspaceId, candidate.sourceTransactionId),
    enabled: !!workspaceId,
  });

  const destinationsQuery = useQuery({
    queryKey: [
      "transactions-by-ids",
      workspaceId,
      candidate.candidateDestinationIds,
    ],
    queryFn: () =>
      Promise.all(
        candidate.candidateDestinationIds.map((id) =>
          fetchTransaction(workspaceId, id),
        ),
      ),
    enabled: candidate.candidateDestinationIds.length > 0,
  });

  const pair = useMutation({
    mutationFn: (destId: string) =>
      manualPairTransfer(workspaceId, {
        sourceId: candidate.sourceTransactionId,
        destinationId: destId,
      }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["transactions", workspaceId],
        }),
        queryClient.invalidateQueries({
          queryKey: ["transfer-candidate-count", workspaceId],
        }),
      ]);
      onDone();
    },
  });

  const decline = useMutation({
    mutationFn: () => declineTransferCandidate(workspaceId, candidate.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["transfer-candidate-count", workspaceId],
      });
      onDone();
    },
  });

  const source = sourceQuery.data;
  const destinations = destinationsQuery.data ?? [];

  const padding = mode === "page" ? "p-4" : "p-3";
  return (
    <Card className={padding}>
      <div className="text-fg-faint mb-1 text-[11px] uppercase tracking-[0.07em]">
        Source
      </div>
      <div className="text-fg mb-3 text-[13px]">
        {source ? formatTxLine(source) : "loading…"}
      </div>
      <div className="text-fg-faint mb-1 text-[11px] uppercase tracking-[0.07em]">
        Pick a counterpart
      </div>
      <div className="mb-3 flex flex-col gap-1.5">
        {destinations.map((d) => (
          <label
            key={d.id}
            className="flex cursor-pointer items-center gap-2 text-[12px]"
          >
            <input
              type="radio"
              name={`cand-${candidate.id}`}
              value={d.id}
              checked={chosenDestId === d.id}
              onChange={() => setChosenDestId(d.id)}
            />
            <span className="text-fg-muted">{formatTxLine(d)}</span>
          </label>
        ))}
        {destinations.length === 0 && !destinationsQuery.isLoading ? (
          <span className="text-fg-faint text-[12px]">No candidates left</span>
        ) : null}
      </div>
      <div className="flex justify-end gap-2">
        <Button
          size="sm"
          variant="secondary"
          disabled={decline.isPending}
          onClick={() => decline.mutate()}
        >
          External credit
        </Button>
        <Button
          size="sm"
          disabled={!chosenDestId || pair.isPending}
          onClick={() => chosenDestId && pair.mutate(chosenDestId)}
        >
          Pair selected
        </Button>
      </div>
    </Card>
  );
}

function formatTxLine(t: Transaction) {
  const date = new Date(t.bookedAt).toISOString().slice(0, 10);
  const desc = t.counterpartyRaw ?? t.description ?? "—";
  return `${date} · ${desc} · ${t.amount} ${t.currency}`;
}

"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  fetchAccounts,
  fetchTransactionsWithTransfers,
  manualPairTransfer,
  type Account,
  type Transaction,
  type TransactionWithTransfer,
} from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { FormError } from "@/components/ui/form-error";

export type ManualPairDialogProps = {
  open: boolean;
  workspaceId: string;
  source: Transaction;
  onClose: () => void;
};

export function ManualPairDialog(props: ManualPairDialogProps) {
  return (
    <Dialog
      open={props.open}
      onOpenChange={(next) => {
        if (!next) props.onClose();
      }}
    >
      {/* Remount the body when the dialog opens (or the source changes) so
          internal form state — search term, chosen candidate, external toggle —
          resets without a setState inside an effect. */}
      {props.open ? (
        <ManualPairDialogBody key={props.source.id} {...props} />
      ) : null}
    </Dialog>
  );
}

function ManualPairDialogBody({
  workspaceId,
  source,
  onClose,
}: ManualPairDialogProps) {
  const queryClient = useQueryClient();

  const [search, setSearch] = React.useState("");
  const [chosenId, setChosenId] = React.useState<string | null>(null);
  const [external, setExternal] = React.useState(false);

  const accountsQuery = useQuery({
    queryKey: ["accounts", workspaceId, true],
    queryFn: () => fetchAccounts(workspaceId, { includeArchived: true }),
    enabled: !!workspaceId,
  });

  const candidatesQuery = useQuery({
    queryKey: ["transactions-pair-candidates", workspaceId, source.id],
    queryFn: () =>
      fetchTransactionsWithTransfers(workspaceId, {
        hideInternalMoves: true,
        limit: 50,
      }),
    enabled: !!workspaceId,
  });

  const accountById = React.useMemo(
    () =>
      new Map<string, Account>(
        (accountsQuery.data ?? []).map((a) => [a.id, a])
      ),
    [accountsQuery.data]
  );

  const sourceAmount = parseFloat(source.amount);

  const candidates = React.useMemo<TransactionWithTransfer[]>(() => {
    const all = candidatesQuery.data ?? [];
    const term = search.trim().toLowerCase();
    return all.filter((t) => {
      if (t.id === source.id) return false;
      if (t.accountId === source.accountId) return false;
      const a = parseFloat(t.amount);
      if (Number.isNaN(a) || Number.isNaN(sourceAmount)) return false;
      // Opposite-sign requirement: source negative => candidate must be > 0
      // (and vice versa). Zero on either side is rejected.
      if (sourceAmount < 0 && a <= 0) return false;
      if (sourceAmount > 0 && a >= 0) return false;
      if (term.length > 0) {
        const hay = (t.counterpartyRaw ?? t.description ?? "").toLowerCase();
        if (!hay.includes(term)) return false;
      }
      return true;
    });
  }, [candidatesQuery.data, search, source.id, source.accountId, sourceAmount]);

  const mutation = useMutation({
    mutationFn: () =>
      manualPairTransfer(workspaceId, {
        sourceId: source.id,
        destinationId: external ? null : chosenId,
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
      onClose();
    },
  });

  const apiError =
    mutation.error instanceof ApiError
      ? mutation.error.message
      : mutation.error
        ? "Couldn't pair transactions. Please try again."
        : null;

  const candidatesError =
    candidatesQuery.error instanceof ApiError
      ? candidatesQuery.error.message
      : candidatesQuery.error
        ? "Couldn't load candidate transactions."
        : null;

  const confirmDisabled =
    mutation.isPending || (!external && !chosenId);

  const sourceAccountLabel =
    accountById.get(source.accountId)?.name ?? source.accountId.slice(0, 8);

  return (
    <DialogContent
      className="flex max-w-lg flex-col gap-4 p-5"
      onInteractOutside={(event) => {
        if (mutation.isPending) event.preventDefault();
      }}
      onEscapeKeyDown={(event) => {
        if (mutation.isPending) event.preventDefault();
      }}
    >
      <DialogHeader>
        <DialogTitle>Pair this transaction with another</DialogTitle>
        <DialogDescription>
          <span>{source.bookedAt.slice(0, 10)}</span>
          <span className="text-fg-faint"> · </span>
          <span>{sourceAccountLabel}</span>
          <span className="text-fg-faint"> · </span>
          <span className="tabular-nums">
            {source.amount} {source.currency}
          </span>
        </DialogDescription>
      </DialogHeader>

        <label className="flex items-start gap-2 rounded-[8px] border border-border bg-surface-subtle px-3 py-2 text-[12px] text-fg-muted">
          <input
            type="checkbox"
            className="mt-0.5 h-3.5 w-3.5 accent-accent"
            checked={external}
            onChange={(event) => {
              setExternal(event.target.checked);
              if (event.target.checked) setChosenId(null);
            }}
            disabled={mutation.isPending}
          />
          <span>
            Mark as outbound to an external (untracked) account.{" "}
            <span className="text-fg-faint">
              No counterpart in Folio is needed.
            </span>
          </span>
        </label>

        {!external ? (
          <>
            <Field label="Search candidates" htmlFor="manual-pair-search">
              <Input
                id="manual-pair-search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="counterparty / description"
                autoComplete="off"
                disabled={mutation.isPending}
              />
            </Field>
            <div className="max-h-64 overflow-y-auto rounded-[8px] border border-border">
              {candidatesQuery.isLoading ? (
                <div className="px-3 py-3 text-[12px] text-fg-muted">
                  Loading candidates…
                </div>
              ) : candidatesError ? (
                <div className="px-3 py-3 text-[12px] text-danger">
                  {candidatesError}
                </div>
              ) : candidates.length === 0 ? (
                <div className="px-3 py-3 text-[12px] text-fg-muted">
                  {search.trim().length === 0
                    ? "No unpaired candidates with the opposite sign in another account."
                    : "No candidates match your search."}
                </div>
              ) : (
                <ul className="divide-y divide-border">
                  {candidates.map((c) => {
                    const accountLabel =
                      accountById.get(c.accountId)?.name ??
                      c.accountId.slice(0, 8);
                    const id = `manual-pair-candidate-${c.id}`;
                    return (
                      <li key={c.id}>
                        <label
                          htmlFor={id}
                          className="flex cursor-pointer items-start gap-2 px-3 py-2 text-[12px] hover:bg-surface-subtle focus-within:bg-surface-subtle"
                        >
                          <input
                            id={id}
                            type="radio"
                            name="manual-pair-target"
                            value={c.id}
                            checked={chosenId === c.id}
                            onChange={() => setChosenId(c.id)}
                            disabled={mutation.isPending}
                            className="mt-0.5 h-3.5 w-3.5 accent-accent"
                          />
                          <span className="flex min-w-0 flex-col">
                            <span className="text-fg">
                              <span>{c.bookedAt.slice(0, 10)}</span>
                              <span className="text-fg-faint"> · </span>
                              <span>{accountLabel}</span>
                              <span className="text-fg-faint"> · </span>
                              <span className="tabular-nums">
                                {c.amount} {c.currency}
                              </span>
                            </span>
                            <span className="truncate text-[11px] text-fg-faint">
                              {c.counterpartyRaw ?? c.description ?? "—"}
                            </span>
                          </span>
                        </label>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>
          </>
        ) : null}

        {apiError ? <FormError>{apiError}</FormError> : null}

        <div className="mt-1 flex flex-col-reverse items-stretch gap-2 sm:flex-row sm:items-center sm:justify-end">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={onClose}
            disabled={mutation.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
            size="sm"
            onClick={() => mutation.mutate()}
            disabled={confirmDisabled}
          >
            {mutation.isPending
              ? "Pairing…"
              : external
                ? "Mark as external"
                : "Pair selected"}
          </Button>
        </div>
    </DialogContent>
  );
}

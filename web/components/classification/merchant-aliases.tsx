"use client";

import * as React from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { FormError } from "@/components/ui/form-error";
import { Input } from "@/components/ui/input";
import {
  ApiError,
  addMerchantAlias,
  removeMerchantAlias,
  type MerchantAlias,
} from "@/lib/api/client";

export function MerchantAliasesPanel({
  workspaceId,
  merchantId,
  aliases,
  isLoading,
  isError,
}: {
  workspaceId: string;
  merchantId: string;
  aliases: MerchantAlias[];
  isLoading: boolean;
  isError: boolean;
}) {
  const queryClient = useQueryClient();
  const [pattern, setPattern] = React.useState("");

  const invalidate = React.useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: ["merchant-aliases", workspaceId, merchantId],
      }),
      queryClient.invalidateQueries({
        queryKey: ["transactions", workspaceId],
      }),
    ]);
  }, [queryClient, workspaceId, merchantId]);

  const addMutation = useMutation({
    mutationFn: async (rawPattern: string) =>
      addMerchantAlias(workspaceId, merchantId, { rawPattern }),
    onSuccess: async () => {
      setPattern("");
      await invalidate();
    },
  });

  const removeMutation = useMutation({
    mutationFn: async (aliasId: string) =>
      removeMerchantAlias(workspaceId, merchantId, aliasId),
    onSuccess: async () => {
      await invalidate();
    },
  });

  const addError =
    addMutation.error instanceof ApiError ? addMutation.error.message : null;

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-border px-5 py-3">
        <div className="text-[13px] font-medium text-fg">
          Aliases
          {aliases.length > 0 ? (
            <span className="ml-2 text-fg-faint tabular-nums">
              ({aliases.length})
            </span>
          ) : null}
        </div>
        <p className="text-[12px] text-fg-faint">
          Raw counterparty strings that match this merchant during import.
        </p>
      </div>

      {isError ? (
        <div className="px-5 py-4">
          <ErrorBanner
            title="Couldn't load aliases"
            description="Check that the backend is running."
          />
        </div>
      ) : isLoading ? (
        <div className="px-5 py-4">
          <LoadingText />
        </div>
      ) : aliases.length === 0 ? (
        <div className="px-5 py-4 text-[13px] text-fg-muted">
          No aliases yet. Add a raw counterparty pattern below to capture
          incoming transactions automatically.
        </div>
      ) : (
        <ul className="divide-y divide-border">
          {aliases.map((alias) => (
            <li
              key={alias.id}
              className="flex items-center justify-between gap-3 px-5 py-2.5 text-[13px]"
            >
              <span className="truncate font-mono text-[12px] text-fg">
                {alias.rawPattern}
              </span>
              <Button
                variant="ghost"
                size="icon"
                disabled={removeMutation.isPending}
                onClick={() => removeMutation.mutate(alias.id)}
                aria-label="Remove alias"
              >
                <X className="h-4 w-4" />
                <span className="sr-only">Remove alias</span>
              </Button>
            </li>
          ))}
        </ul>
      )}

      <form
        className="flex flex-col gap-2 border-t border-border px-5 py-4"
        onSubmit={(event) => {
          event.preventDefault();
          const trimmed = pattern.trim();
          if (!trimmed) return;
          addMutation.mutate(trimmed);
        }}
      >
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            value={pattern}
            onChange={(event) => setPattern(event.target.value)}
            placeholder="e.g. COOP-4382 ZUR"
            aria-label="New alias pattern"
            className="sm:flex-1"
          />
          <Button
            type="submit"
            size="sm"
            disabled={addMutation.isPending || !pattern.trim()}
          >
            <Plus className="h-3.5 w-3.5" />
            Add alias
          </Button>
        </div>
        {addError ? <FormError>{addError}</FormError> : null}
      </form>
    </Card>
  );
}

"use client";

import * as React from "react";
import { FileUp, Upload } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ErrorBanner } from "@/components/app/empty";
import {
  uploadInvestmentImport,
  type ImportFormat,
  type ImportSummary,
} from "@/lib/api/investments";
// eslint-disable-next-line @typescript-eslint/no-unused-vars
type _Account = Account;
import { fetchAccounts, type Account } from "@/lib/api/client";
import { useQuery } from "@tanstack/react-query";

export function ImportInvestmentsCard({
  workspaceId,
  onClose,
}: {
  workspaceId: string;
  onClose: () => void;
}) {
  const [format, setFormat] = React.useState<ImportFormat>("ibkr");
  const [pickedAccountId, setPickedAccountId] = React.useState<string>("");
  const [file, setFile] = React.useState<File | null>(null);
  const [summary, setSummary] = React.useState<ImportSummary | null>(null);
  const queryClient = useQueryClient();

  const accountsQuery = useQuery({
    queryKey: ["accounts", workspaceId],
    queryFn: () => fetchAccounts(workspaceId),
  });
  const brokerageAccounts: Account[] =
    accountsQuery.data?.filter((a) => a.kind === "brokerage") ?? [];

  // Default the dropdown to the first brokerage account; user override wins.
  const accountId = pickedAccountId || brokerageAccounts[0]?.id || "";

  const uploadMutation = useMutation({
    mutationFn: () => {
      if (!file || !accountId) {
        throw new Error("Pick a file and account");
      }
      return uploadInvestmentImport(workspaceId, format, accountId, file);
    },
    onSuccess: (res) => {
      setSummary(res);
      queryClient.invalidateQueries({ queryKey: ["investments"] });
    },
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Import investment activity</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="flex flex-col gap-1">
            <Label htmlFor="import-format">Format</Label>
            <select
              id="import-format"
              className="rounded-[8px] border border-border bg-surface px-3 py-1.5 text-[13px]"
              value={format}
              onChange={(e) => setFormat(e.target.value as ImportFormat)}
            >
              <option value="ibkr">Interactive Brokers (Activity CSV / JSON)</option>
              <option value="revolut_trading">Revolut Trading (CSV)</option>
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="import-account">Account</Label>
            <select
              id="import-account"
              className="rounded-[8px] border border-border bg-surface px-3 py-1.5 text-[13px]"
              value={accountId}
              onChange={(e) => setPickedAccountId(e.target.value)}
            >
              <option value="">— pick a brokerage account —</option>
              {brokerageAccounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} ({a.currency})
                </option>
              ))}
            </select>
            {brokerageAccounts.length === 0 && !accountsQuery.isLoading ? (
              <p className="text-[11px] text-fg-muted">
                Open a brokerage account first.
              </p>
            ) : null}
          </div>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="import-file">File</Label>
          <Input
            id="import-file"
            type="file"
            accept=".csv,.json,text/csv,application/json"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
        </div>

        {uploadMutation.isError ? (
          <ErrorBanner
            title="Import failed"
            description={(uploadMutation.error as Error).message}
          />
        ) : null}

        {summary ? (
          <div className="rounded-[12px] border border-border bg-surface p-4 text-[13px]">
            <p className="font-medium text-fg">Import succeeded.</p>
            <ul className="mt-2 space-y-0.5 text-fg-muted">
              <li>
                Trades created: <b className="text-fg">{summary.tradesCreated}</b>
              </li>
              <li>
                Dividends created: <b className="text-fg">{summary.dividendsCreated}</b>
              </li>
              <li>
                Instruments touched: <b className="text-fg">{summary.instrumentsTouched}</b>
              </li>
              {summary.skipped > 0 ? (
                <li>
                  Skipped rows: <b className="text-fg">{summary.skipped}</b>
                </li>
              ) : null}
            </ul>
            {summary.warnings && summary.warnings.length > 0 ? (
              <details className="mt-2">
                <summary className="cursor-pointer text-[12px] text-fg-muted">
                  {summary.warnings.length} warning(s)
                </summary>
                <ul className="mt-2 list-disc pl-5 text-[12px] text-fg-muted">
                  {summary.warnings.map((w, i) => (
                    <li key={i}>{w}</li>
                  ))}
                </ul>
              </details>
            ) : null}
          </div>
        ) : null}

        <div className="flex items-center justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
          <Button
            onClick={() => uploadMutation.mutate()}
            disabled={!file || !accountId || uploadMutation.isPending}
          >
            <Upload className="h-4 w-4" />
            {uploadMutation.isPending ? "Uploading…" : "Upload"}
          </Button>
        </div>

        <p className="text-[11px] text-fg-muted">
          IBKR: paste an Activity CSV or the legacy JSON dump. Revolut: the
          Trading export from the app (Statements → CSV). Imports add events on
          top of existing data — re-importing the same file may create duplicates.
        </p>
      </CardContent>
    </Card>
  );
}

export function ImportInvestmentsButton({
  workspaceId,
}: {
  workspaceId: string;
}) {
  const [open, setOpen] = React.useState(false);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen((v) => !v)}>
        <FileUp className="h-4 w-4" />
        {open ? "Close import" : "Import"}
      </Button>
      {open ? (
        <div className="col-span-full">
          <ImportInvestmentsCard
            workspaceId={workspaceId}
            onClose={() => setOpen(false)}
          />
        </div>
      ) : null}
    </>
  );
}

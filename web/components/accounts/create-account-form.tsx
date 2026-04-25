"use client";

import * as React from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  applyAccountImport,
  createAccount,
  previewAccountImport,
  type Account,
  type ImportPreview,
} from "@/lib/api/client";
import { ACCOUNT_KINDS } from "@/lib/accounts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/ui/field";
import { cn } from "@/lib/utils";

// Keep amounts as strings - backend owns the decimal math.
const amountRegex = /^-?\d+(\.\d+)?$/;

const schema = z
  .object({
    name: z.string().trim().min(1, "Required"),
    kind: z.string().min(1, "Pick a type"),
    currency: z
      .string()
      .trim()
      .regex(/^[a-zA-Z]{3}$/, "ISO 4217 code, e.g. CHF"),
    institution: z.string().trim().max(120).optional().or(z.literal("")),
    nickname: z.string().trim().max(80).optional().or(z.literal("")),
    openDate: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, "Use YYYY-MM-DD"),
    openingBalance: z
      .string()
      .trim()
      .regex(amountRegex, "Decimal like 0 or 1234.50"),
    openingBalanceDate: z
      .string()
      .regex(/^\d{4}-\d{2}-\d{2}$/, "Use YYYY-MM-DD")
      .optional()
      .or(z.literal("")),
  })
  .superRefine((val, ctx) => {
    if (val.openingBalanceDate && val.openingBalanceDate < val.openDate) {
      ctx.addIssue({
        path: ["openingBalanceDate"],
        code: z.ZodIssueCode.custom,
        message: "Must be on or after open date",
      });
    }
  });

type FormValues = z.infer<typeof schema>;

export function CreateAccountForm({
  tenantId,
  defaultCurrency,
  onCreated,
  onCancel,
}: {
  tenantId: string;
  defaultCurrency: string;
  onCreated?: (a: Account) => void;
  onCancel?: () => void;
}) {
  const qc = useQueryClient();
  const today = React.useMemo(() => new Date().toISOString().slice(0, 10), []);
  const [preview, setPreview] = React.useState<ImportPreview | null>(null);
  const [createdAccount, setCreatedAccount] = React.useState<Account | null>(null);

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: "",
      kind: "checking",
      currency: defaultCurrency.toUpperCase(),
      institution: "",
      nickname: "",
      openDate: today,
      openingBalance: "0",
      openingBalanceDate: today,
    },
    mode: "onBlur",
  });

  const mutation = useMutation({
    mutationFn: (values: FormValues) =>
      createAccount(tenantId, {
        name: values.name,
        kind: values.kind as Account["kind"],
        currency: values.currency.toUpperCase(),
        institution: values.institution ? values.institution : null,
        nickname: values.nickname ? values.nickname : null,
        openDate: values.openDate,
        openingBalance: values.openingBalance,
        openingBalanceDate: values.openingBalanceDate || values.openDate,
      }),
    onSuccess: (acc) => {
      qc.invalidateQueries({ queryKey: ["accounts", tenantId] });
      if (preview) {
        setCreatedAccount(acc);
        return;
      }
      form.reset({
        ...form.getValues(),
        name: "",
        institution: "",
        nickname: "",
        openingBalance: "0",
      });
      onCreated?.(acc);
    },
  });

  const previewMutation = useMutation({
    mutationFn: (file: File) => previewAccountImport(tenantId, file),
    onSuccess: (p) => {
      setPreview(p);
      form.setValue("currency", p.suggestedCurrency ?? defaultCurrency, {
        shouldValidate: true,
      });
      if (p.suggestedName) {
        form.setValue("name", p.suggestedName, { shouldValidate: true });
      }
      if (p.institution) {
        form.setValue("institution", p.institution, { shouldValidate: true });
      }
      if (p.suggestedKind) {
        form.setValue("kind", p.suggestedKind, { shouldValidate: true });
      }
      if (p.suggestedOpenDate) {
        form.setValue("openDate", p.suggestedOpenDate, {
          shouldValidate: true,
        });
        form.setValue("openingBalanceDate", p.suggestedOpenDate, {
          shouldValidate: true,
        });
      }
    },
  });

  const importMutation = useMutation({
    mutationFn: () =>
      applyAccountImport(tenantId, createdAccount!.id, preview!.fileToken),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["accounts", tenantId] });
      await qc.invalidateQueries({ queryKey: ["transactions", tenantId] });
      finish(createdAccount!);
    },
  });

  function finish(acc: Account) {
    form.reset({
      ...form.getValues(),
      name: "",
      institution: "",
      nickname: "",
      openingBalance: "0",
    });
    setPreview(null);
    setCreatedAccount(null);
    onCreated?.(acc);
  }

  const err = mutation.error instanceof ApiError ? mutation.error : null;
  const previewErr =
    previewMutation.error instanceof ApiError ? previewMutation.error : null;
  const importErr =
    importMutation.error instanceof ApiError ? importMutation.error : null;

  if (createdAccount && preview) {
    return (
      <div className="flex flex-col gap-5">
        <div className="rounded-[12px] border border-[--color-border] bg-[--color-surface] px-5 py-4">
          <h3 className="text-[15px] font-medium">Import transactions?</h3>
          <p className="mt-1 text-[13px] text-[--color-fg-muted]">
            {preview.fileName || "This export"} contains{" "}
            <span className="tabular">{preview.transactionCount}</span>{" "}
            transactions from {preview.dateFrom || "the first entry"} to{" "}
            {preview.dateTo || "the last entry"}.
          </p>
          <div className="mt-3 grid gap-2 text-[12px] text-[--color-fg-muted] sm:grid-cols-3">
            <span>
              Importable:{" "}
              <strong className="tabular text-[--color-fg]">
                {preview.importableCount}
              </strong>
            </span>
            <span>
              Duplicates:{" "}
              <strong className="tabular text-[--color-fg]">
                {preview.duplicateCount}
              </strong>
            </span>
            <span>
              Review:{" "}
              <strong className="tabular text-[--color-fg]">
                {preview.conflictCount}
              </strong>
            </span>
          </div>
          {importErr ? (
            <div className="mt-3 rounded-[8px] border border-border bg-[#F5DADA] px-3 py-2 text-[13px] text-danger">
              {importErr.body?.error || importErr.message}
            </div>
          ) : null}
        </div>
        <div className="flex items-center justify-end gap-2">
          <Button
            type="button"
            variant="secondary"
            onClick={() => finish(createdAccount)}
            disabled={importMutation.isPending}
          >
            Not now
          </Button>
          <Button
            type="button"
            onClick={() => importMutation.mutate()}
            disabled={importMutation.isPending || preview.importableCount === 0}
          >
            {importMutation.isPending ? "Importing..." : "Import transactions"}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={form.handleSubmit((v) => mutation.mutate(v))}
      noValidate
    >
      <div className="grid gap-5 sm:grid-cols-2">
        <Field
          label="Name"
          htmlFor="acc-name"
          error={form.formState.errors.name?.message}
        >
          <Input id="acc-name" {...form.register("name")} />
        </Field>

        <Field
          label="Type"
          htmlFor="acc-kind"
          error={form.formState.errors.kind?.message}
        >
          <select
            id="acc-kind"
            className={cn(
              "h-9 w-full rounded-[8px] border border-border bg-surface px-3 text-[14px]",
              "focus:border-border-strong focus:ring-2 focus:ring-accent focus:outline-none"
            )}
            {...form.register("kind")}
          >
            {ACCOUNT_KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <div className="grid gap-5 sm:grid-cols-3">
        <Field
          label="Currency"
          htmlFor="acc-ccy"
          error={form.formState.errors.currency?.message}
        >
          <Input
            id="acc-ccy"
            maxLength={3}
            className="tabular uppercase"
            {...form.register("currency")}
          />
        </Field>

        <Field
          label="Institution"
          htmlFor="acc-inst"
          hint="Optional - e.g. UBS, Revolut."
          error={form.formState.errors.institution?.message}
        >
          <Input id="acc-inst" {...form.register("institution")} />
        </Field>

        <Field
          label="Nickname"
          htmlFor="acc-nick"
          hint="Optional label shown alongside the name."
          error={form.formState.errors.nickname?.message}
        >
          <Input id="acc-nick" {...form.register("nickname")} />
        </Field>
      </div>

      <Field
        label="Bank export"
        htmlFor="acc-import"
        hint="Optional CSV export. Folio will prefill what it can before creating the account."
      >
        <Input
          id="acc-import"
          type="file"
          accept=".csv,text/csv"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            previewMutation.mutate(file);
          }}
        />
      </Field>

      {previewMutation.isPending ? (
        <p className="text-[13px] text-[--color-fg-muted]">
          Reading bank export...
        </p>
      ) : null}

      {preview ? (
        <div className="rounded-[12px] border border-[--color-border] bg-[--color-surface] px-4 py-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <p className="text-[13px] font-medium text-[--color-fg]">
                {preview.institution || "Bank export"} detected
              </p>
              <p className="text-[12px] text-[--color-fg-muted]">
                <span className="tabular">{preview.transactionCount}</span>{" "}
                transactions
                {preview.dateFrom && preview.dateTo
                  ? ` · ${preview.dateFrom} to ${preview.dateTo}`
                  : ""}
              </p>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setPreview(null)}
            >
              Remove
            </Button>
          </div>
          {preview.warnings?.length ? (
            <ul className="mt-2 list-disc pl-4 text-[12px] text-[--color-fg-muted]">
              {preview.warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {previewErr ? (
        <div className="rounded-[8px] border border-border bg-[#F5DADA] px-3 py-2 text-[13px] text-danger">
          {previewErr.body?.error || previewErr.message}
        </div>
      ) : null}

      <div className="grid gap-5 sm:grid-cols-3">
        <Field
          label="Opening balance"
          htmlFor="acc-ob"
          hint="Decimal string in the account currency."
          error={form.formState.errors.openingBalance?.message}
        >
          <Input
            id="acc-ob"
            className="tabular"
            inputMode="decimal"
            {...form.register("openingBalance")}
          />
        </Field>

        <Field
          label="Open date"
          htmlFor="acc-open"
          error={form.formState.errors.openDate?.message}
        >
          <Input
            id="acc-open"
            type="date"
            className="tabular"
            {...form.register("openDate")}
          />
        </Field>

        <Field
          label="Opening balance date"
          htmlFor="acc-obd"
          hint="Defaults to open date."
          error={form.formState.errors.openingBalanceDate?.message}
        >
          <Input
            id="acc-obd"
            type="date"
            className="tabular"
            {...form.register("openingBalanceDate")}
          />
        </Field>
      </div>

      {err ? (
        <div className="rounded-[8px] border border-border bg-[#F5DADA] px-3 py-2 text-[13px] text-danger">
          {err.body?.error || err.message}
        </div>
      ) : null}

      <div className="flex items-center justify-end gap-2">
        {onCancel ? (
          <Button type="button" variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
        <Button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? "Adding..." : "Add account"}
        </Button>
      </div>
    </form>
  );
}

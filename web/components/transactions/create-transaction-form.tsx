"use client";

import * as React from "react";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  createTransaction,
  type Account,
  type Transaction,
} from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/ui/field";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { FormError } from "@/components/ui/form-error";
import { AMOUNT_REGEX } from "@/lib/decimal";

const schema = z.object({
  accountId: z.string().uuid("Pick an account"),
  bookedAt: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, "Use YYYY-MM-DD"),
  amount: z
    .string()
    .trim()
    .regex(AMOUNT_REGEX, "Decimal like 42.50 or -42.50")
    .refine((v) => v !== "0" && v !== "-0", "Amount can't be zero"),
  description: z.string().trim().max(200).optional().or(z.literal("")),
  counterpartyRaw: z.string().trim().max(200).optional().or(z.literal("")),
  notes: z.string().trim().max(2000).optional().or(z.literal("")),
});

type FormValues = z.infer<typeof schema>;

export function CreateTransactionForm({
  workspaceId,
  accounts,
  onCreated,
  onCancel,
}: {
  workspaceId: string;
  accounts: Account[];
  onCreated?: (t: Transaction) => void;
  onCancel?: () => void;
}) {
  const qc = useQueryClient();
  const today = React.useMemo(() => new Date().toISOString().slice(0, 10), []);
  const firstAccount = accounts[0];

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      accountId: firstAccount?.id ?? "",
      bookedAt: today,
      amount: "",
      description: "",
      counterpartyRaw: "",
      notes: "",
    },
    mode: "onBlur",
  });

  const selectedAccountId = useWatch({
    control: form.control,
    name: "accountId",
  });
  const selectedAccount = accounts.find((a) => a.id === selectedAccountId);

  const mutation = useMutation({
    mutationFn: (values: FormValues) => {
      const account = accounts.find((a) => a.id === values.accountId);
      if (!account) throw new Error("account not found");
      return createTransaction(workspaceId, {
        accountId: values.accountId,
        status: "posted",
        bookedAt: values.bookedAt,
        amount: values.amount,
        currency: account.currency,
        description: values.description || null,
        counterpartyRaw: values.counterpartyRaw || null,
        notes: values.notes || null,
      });
    },
    onSuccess: (tx) => {
      qc.invalidateQueries({ queryKey: ["transactions", workspaceId] });
      qc.invalidateQueries({ queryKey: ["accounts", workspaceId] });
      form.reset({
        ...form.getValues(),
        amount: "",
        description: "",
        counterpartyRaw: "",
        notes: "",
      });
      onCreated?.(tx);
    },
  });

  const err = mutation.error instanceof ApiError ? mutation.error : null;

  if (accounts.length === 0) {
    return (
      <p className="text-[13px] text-fg-muted">
        Add an account before recording transactions.
      </p>
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
          label="Account"
          htmlFor="tx-account"
          error={form.formState.errors.accountId?.message}
          hint={
            selectedAccount
              ? `Transactions post in ${selectedAccount.currency}.`
              : undefined
          }
        >
          <Select id="tx-account" {...form.register("accountId")}>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} - {a.currency}
              </option>
            ))}
          </Select>
        </Field>

        <Field
          label="Booked date"
          htmlFor="tx-date"
          error={form.formState.errors.bookedAt?.message}
        >
          <Input
            id="tx-date"
            type="date"
            className="tabular"
            {...form.register("bookedAt")}
          />
        </Field>
      </div>

      <div className="grid gap-5 sm:grid-cols-2">
        <Field
          label="Amount"
          htmlFor="tx-amount"
          hint="Negative for expenses, positive for income."
          error={form.formState.errors.amount?.message}
        >
          <Input
            id="tx-amount"
            inputMode="decimal"
            placeholder="-42.50"
            className="tabular"
            {...form.register("amount")}
          />
        </Field>

        <Field
          label="Counterparty (raw)"
          htmlFor="tx-cp"
          hint="Merchant string as it would appear on a statement."
          error={form.formState.errors.counterpartyRaw?.message}
        >
          <Input id="tx-cp" {...form.register("counterpartyRaw")} />
        </Field>
      </div>

      <Field
        label="Description"
        htmlFor="tx-desc"
        error={form.formState.errors.description?.message}
      >
        <Input id="tx-desc" {...form.register("description")} />
      </Field>

      <Field
        label="Notes"
        htmlFor="tx-notes"
        error={form.formState.errors.notes?.message}
      >
        <Textarea id="tx-notes" rows={3} {...form.register("notes")} />
      </Field>

      {err ? <FormError>{err.body?.error || err.message}</FormError> : null}

      <div className="flex items-center justify-end gap-2">
        {onCancel ? (
          <Button type="button" variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
        <Button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? "Adding..." : "Record transaction"}
        </Button>
      </div>
    </form>
  );
}

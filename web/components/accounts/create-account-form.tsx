"use client";

import * as React from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, createAccount, type Account } from "@/lib/api/client";
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

  const err = mutation.error instanceof ApiError ? mutation.error : null;

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
              "h-9 w-full rounded-[8px] border border-[--color-border] bg-[--color-surface] px-3 text-[14px]",
              "focus:border-[--color-border-strong] focus:ring-2 focus:ring-[--color-accent] focus:outline-none"
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
        <div className="rounded-[8px] border border-[--color-border] bg-[#F5DADA] px-3 py-2 text-[13px] text-[--color-danger]">
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

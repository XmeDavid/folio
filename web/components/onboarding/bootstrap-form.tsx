"use client";

import * as React from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useMutation } from "@tanstack/react-query";
import { ApiError, bootstrapTenant } from "@/lib/api/client";
import { saveIdentity } from "@/lib/tenant";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/ui/field";
import { Badge } from "@/components/ui/badge";

const schema = z.object({
  displayName: z.string().trim().min(1, "Your name is required").max(80),
  email: z.string().trim().toLowerCase().email("Must look like an email"),
  tenantName: z
    .string()
    .trim()
    .min(1, "Give your workspace a name (e.g. 'Household')"),
  baseCurrency: z
    .string()
    .trim()
    .regex(/^[a-zA-Z]{3}$/, "ISO 4217 code, e.g. CHF"),
  locale: z.string().trim().min(2, "e.g. en-CH"),
  timezone: z.string().trim().min(1),
  cycleAnchorDay: z
    .number({ invalid_type_error: "Must be a number" })
    .int()
    .min(1)
    .max(31),
});

type FormValues = z.infer<typeof schema>;

export function BootstrapForm() {
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      displayName: "",
      email: "",
      tenantName: "",
      baseCurrency: "CHF",
      locale: "en-CH",
      timezone: "Europe/Zurich",
      cycleAnchorDay: 1,
    },
    mode: "onBlur",
  });

  const mutation = useMutation({
    mutationFn: (values: FormValues) =>
      bootstrapTenant({
        ...values,
        baseCurrency: values.baseCurrency.toUpperCase(),
      }),
    onSuccess: (res) => {
      saveIdentity(res.tenant.id, res.user.id);
    },
  });

  const err = mutation.error instanceof ApiError ? mutation.error : null;

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-3xl flex-col justify-center gap-8 px-6 py-12">
      <header className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <div
            aria-hidden
            className="h-6 w-6 rounded-[6px] bg-[--color-accent]"
          />
          <span className="text-[13px] font-medium tracking-tight">Folio</span>
          <Badge variant="amber" className="ml-2">
            Dev bridge
          </Badge>
        </div>
        <h1 className="text-[28px] leading-tight font-normal tracking-tight">
          Set up your workspace
        </h1>
        <p className="max-w-xl text-[14px] text-[--color-fg-muted]">
          Folio bootstraps a tenant and a single user. Real session auth is not
          wired up yet - the tenant id returned here is stored in{" "}
          <span className="font-medium text-[--color-fg]">localStorage</span>{" "}
          and echoed as the <code>X-Tenant-ID</code> header for every
          tenant-scoped request.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Workspace details</CardTitle>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-5"
            onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
            noValidate
          >
            <div className="grid gap-5 sm:grid-cols-2">
              <Field
                label="Your name"
                htmlFor="displayName"
                error={form.formState.errors.displayName?.message}
              >
                <Input
                  id="displayName"
                  autoComplete="name"
                  {...form.register("displayName")}
                />
              </Field>

              <Field
                label="Email"
                htmlFor="email"
                error={form.formState.errors.email?.message}
              >
                <Input
                  id="email"
                  type="email"
                  autoComplete="email"
                  {...form.register("email")}
                />
              </Field>
            </div>

            <Field
              label="Workspace name"
              htmlFor="tenantName"
              hint="What you'd call this Folio - e.g. 'Personal' or 'Household'."
              error={form.formState.errors.tenantName?.message}
            >
              <Input id="tenantName" {...form.register("tenantName")} />
            </Field>

            <div className="grid gap-5 sm:grid-cols-3">
              <Field
                label="Base currency"
                htmlFor="baseCurrency"
                hint="ISO 4217, e.g. CHF, EUR, USD."
                error={form.formState.errors.baseCurrency?.message}
              >
                <Input
                  id="baseCurrency"
                  maxLength={3}
                  autoCapitalize="characters"
                  className="tabular uppercase"
                  {...form.register("baseCurrency")}
                />
              </Field>

              <Field
                label="Cycle anchor day"
                htmlFor="cycleAnchorDay"
                hint="Day of month your payment cycle starts."
                error={form.formState.errors.cycleAnchorDay?.message}
              >
                <Input
                  id="cycleAnchorDay"
                  type="number"
                  min={1}
                  max={31}
                  className="tabular"
                  {...form.register("cycleAnchorDay", { valueAsNumber: true })}
                />
              </Field>

              <Field
                label="Locale"
                htmlFor="locale"
                hint="For number/date formatting."
                error={form.formState.errors.locale?.message}
              >
                <Input id="locale" {...form.register("locale")} />
              </Field>
            </div>

            <Field
              label="Timezone"
              htmlFor="timezone"
              hint="IANA zone, e.g. Europe/Zurich."
              error={form.formState.errors.timezone?.message}
            >
              <Input id="timezone" {...form.register("timezone")} />
            </Field>

            {err ? (
              <div className="rounded-[8px] border border-[--color-border] bg-[#F5DADA] px-3 py-2 text-[13px] text-[--color-danger]">
                {err.body?.error || err.message}
              </div>
            ) : null}

            <div className="flex items-center justify-end gap-3 pt-2">
              <Button
                type="submit"
                disabled={mutation.isPending}
                className="min-w-[160px]"
              >
                {mutation.isPending ? "Creating..." : "Create workspace"}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </main>
  );
}

"use client";

import { Suspense, useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useQueryClient } from "@tanstack/react-query";
import {
  CURRENCY_OPTIONS,
  LANGUAGE_OPTIONS,
  REGION_OPTIONS,
} from "@/lib/localization";
import { ApiError, createWorkspace } from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";

export default function NewWorkspacePage() {
  return (
    <Suspense fallback={null}>
      <NewWorkspaceForm />
    </Suspense>
  );
}

function NewWorkspaceForm() {
  const router = useRouter();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [baseCurrency, setBaseCurrency] = useState("USD");
  const [language, setLanguage] = useState("en");
  const [region, setRegion] = useState("US");
  const [cycleAnchorDay, setCycleAnchorDay] = useState(1);
  const [tz] = useState(() => {
    if (typeof Intl !== "undefined") {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
    }
    return "UTC";
  });
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const result = await createWorkspace({
        name,
        baseCurrency,
        locale: `${language}-${region}`,
        cycleAnchorDay,
        timezone: tz,
      });
      await qc.invalidateQueries({ queryKey: ["me"] });
      const slug = result.workspace.slug;
      router.push((slug ? `/w/${slug}` : "/workspaces") as Route);
    } catch (caught) {
      if (caught instanceof ApiError) {
        setErr(friendlyError(caught.body?.code, caught.body?.error ?? caught.message));
      } else {
        setErr((caught as Error).message ?? "Create workspace failed");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <div>
        <h1 className="text-2xl font-semibold">Create a workspace</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Workspaces let you track finances separately — for example, personal
          and business accounts.
        </p>
      </div>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Field
          label="Workspace name"
          value={name}
          onChange={setName}
          required
          autoComplete="off"
        />
        <Field
          label="Base currency"
          value={baseCurrency}
          onChange={setBaseCurrency}
          options={CURRENCY_OPTIONS}
          required
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            label="Language"
            value={language}
            onChange={setLanguage}
            options={LANGUAGE_OPTIONS}
            required
          />
          <Field
            label="Region"
            value={region}
            onChange={setRegion}
            options={REGION_OPTIONS}
            required
          />
        </div>
        <Field
          label="Cycle anchor day"
          type="number"
          value={String(cycleAnchorDay)}
          onChange={(v) => setCycleAnchorDay(Math.max(1, Math.min(31, Number(v) || 1)))}
          required
          hint="Day of the month your monthly cycle starts."
        />
        <p className="text-xs text-muted-foreground">
          Timezone: <span className="font-medium">{tz}</span> (detected from
          your browser)
        </p>
        {err ? <p className="text-sm text-red-600">{err}</p> : null}
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-foreground px-3 py-2 text-background disabled:opacity-60"
        >
          {busy ? "Creating…" : "Create workspace"}
        </button>
      </form>
      <p className="text-sm text-muted-foreground">
        <a href="/workspaces" className="underline">
          Back to workspaces
        </a>
      </p>
    </main>
  );
}

function Field({
  label,
  value,
  onChange,
  type = "text",
  required,
  autoComplete,
  hint,
  disabled,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  required?: boolean;
  autoComplete?: string;
  hint?: string;
  disabled?: boolean;
  options?: readonly { value: string; label: string }[];
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-sm text-muted-foreground">{label}</span>
      {options ? (
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          required={required}
          disabled={disabled}
          className="rounded border bg-background px-3 py-2 disabled:opacity-60"
        >
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      ) : (
        <input
          type={type}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          required={required}
          autoComplete={autoComplete}
          disabled={disabled}
          min={type === "number" ? 1 : undefined}
          max={type === "number" ? 31 : undefined}
          className="rounded border px-3 py-2 disabled:opacity-60"
        />
      )}
      {hint ? (
        <span className="text-xs text-muted-foreground">{hint}</span>
      ) : null}
    </label>
  );
}

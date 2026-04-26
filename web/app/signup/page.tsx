"use client";

import { Suspense } from "react";
import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import type { Route } from "next";
import { useQueryClient } from "@tanstack/react-query";
import {
  CURRENCY_OPTIONS,
  LANGUAGE_OPTIONS,
  REGION_OPTIONS,
} from "@/lib/localization";

export default function SignupPage() {
  return (
    <Suspense fallback={null}>
      <SignupForm />
    </Suspense>
  );
}

function SignupForm() {
  const sp = useSearchParams();
  const inviteToken = sp.get("inviteToken") ?? undefined;
  const inviteEmail = sp.get("email") ?? "";
  const [email, setEmail] = useState(inviteEmail);
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [baseCurrency, setBaseCurrency] = useState("USD");
  const [language, setLanguage] = useState("en");
  const [region, setRegion] = useState("US");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  const qc = useQueryClient();
  const emailLocked = !!inviteToken && !!inviteEmail;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const locale = `${language}-${region}`;
    try {
      const res = await fetch("/api/v1/auth/signup", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify({
          email,
          password,
          displayName,
          baseCurrency,
          locale,
          inviteToken,
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        setErr(
          (body as { error?: string }).error ?? `Signup failed (${res.status})`,
        );
        return;
      }
      await qc.invalidateQueries({ queryKey: ["me"] });
      const workspace = (body as { workspace?: { slug?: string } }).workspace;
      router.push(
        (workspace?.slug ? `/w/${workspace.slug}` : "/workspaces") as Route,
      );
    } catch (caught) {
      setErr((caught as Error).message ?? "Signup failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Create a Folio account</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Field
          label="Your name"
          value={displayName}
          onChange={setDisplayName}
          required
          autoComplete="name"
        />
        <Field
          label="Email"
          type="email"
          value={email}
          onChange={setEmail}
          required
          autoComplete="email"
          disabled={emailLocked}
          hint={emailLocked ? "Email is locked by your invite." : undefined}
        />
        <Field
          label="Password"
          type="password"
          value={password}
          onChange={setPassword}
          required
          autoComplete="new-password"
          hint="12 characters minimum"
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
        {err ? <p className="text-sm text-red-600">{err}</p> : null}
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-foreground px-3 py-2 text-background"
        >
          {busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="text-sm text-muted-foreground">
        Already have an account?{" "}
        <a href="/login" className="underline">
          Sign in
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
          className="rounded border px-3 py-2 disabled:opacity-60"
        />
      )}
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </label>
  );
}

"use client";

import { useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import type { Route } from "next";
import { useQueryClient } from "@tanstack/react-query";

export default function SignupPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [baseCurrency, setBaseCurrency] = useState("USD");
  const [locale, setLocale] = useState("en-US");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  const sp = useSearchParams();
  const inviteToken = sp.get("inviteToken") ?? undefined;
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
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
      const tenant = (body as { tenant?: { slug?: string } }).tenant;
      router.push(
        (tenant?.slug ? `/t/${tenant.slug}` : "/tenants") as Route,
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
          onChange={(v) => setBaseCurrency(v.toUpperCase())}
          required
        />
        <Field label="Locale" value={locale} onChange={setLocale} required />
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
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  required?: boolean;
  autoComplete?: string;
  hint?: string;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-sm text-muted-foreground">{label}</span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required={required}
        autoComplete={autoComplete}
        className="rounded border px-3 py-2"
      />
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </label>
  );
}

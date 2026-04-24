"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useQueryClient } from "@tanstack/react-query";

export default function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const res = await fetch("/api/v1/auth/login", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify({ email, password }),
      });
      if (res.status === 401) {
        setErr("Invalid email or password.");
        return;
      }
      if (!res.ok) {
        setErr(`Login failed (${res.status})`);
        return;
      }
      await qc.invalidateQueries({ queryKey: ["me"] });
      // Re-fetch /me to decide where to land.
      const meRes = await fetch("/api/v1/me", {
        credentials: "include",
        headers: { "X-Folio-Request": "1" },
      });
      if (meRes.ok) {
        const me = (await meRes.json()) as {
          tenants: Array<{ slug: string }>;
        };
        const slug = me.tenants?.[0]?.slug;
        router.push((slug ? `/t/${slug}` : "/tenants") as Route);
      } else {
        router.push("/tenants" as Route);
      }
    } catch (caught) {
      setErr((caught as Error).message ?? "Login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Sign in to Folio</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">Email</span>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoFocus
            className="rounded border px-3 py-2"
            autoComplete="username webauthn"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className="rounded border px-3 py-2"
            autoComplete="current-password"
          />
        </label>
        {err ? <p className="text-sm text-red-600">{err}</p> : null}
        <button
          type="submit"
          disabled={busy}
          className="rounded bg-foreground px-3 py-2 text-background"
        >
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
      <p className="text-sm text-muted-foreground">
        New here?{" "}
        <a href="/signup" className="underline">
          Create an account
        </a>
      </p>
    </main>
  );
}

"use client";

import { use, useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { confirmPasswordReset } from "@/lib/auth/email-flows";

export default function ResetPage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const router = useRouter();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr(null);
    if (password.length < 12) return setErr("Use at least 12 characters.");
    if (password !== confirm) return setErr("Passwords do not match.");
    try {
      await confirmPasswordReset(token, password);
      router.replace("/login?reset=1" as Route);
    } catch (caught) {
      setErr((caught as Error).message);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Choose a new password</h1>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input className="rounded border px-3 py-2" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
        <input className="rounded border px-3 py-2" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
        {err ? <p className="text-sm text-red-600">{err}</p> : null}
        <button className="rounded bg-foreground px-3 py-2 text-background" type="submit">Reset password</button>
      </form>
    </main>
  );
}

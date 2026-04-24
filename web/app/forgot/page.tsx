"use client";

import { useState } from "react";
import { requestPasswordReset } from "@/lib/auth/email-flows";

export default function ForgotPage() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    await requestPasswordReset(email);
    setSent(true);
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Reset password</h1>
      {sent ? (
        <p className="text-sm text-muted-foreground">If that email exists, a reset link is on its way.</p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-sm text-muted-foreground">Email</span>
            <input className="rounded border px-3 py-2" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
          </label>
          <button className="rounded bg-foreground px-3 py-2 text-background" type="submit">Send reset link</button>
        </form>
      )}
    </main>
  );
}

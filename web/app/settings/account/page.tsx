"use client";

import { useState } from "react";
import { requestEmailChange, resendVerification } from "@/lib/auth/email-flows";
import { useIdentity } from "@/lib/hooks/use-identity";

export default function AccountSettingsPage() {
  const identity = useIdentity();
  const [email, setEmail] = useState("");
  const [msg, setMsg] = useState<string | null>(null);

  if (identity.status !== "authenticated") {
    return <main className="p-6 text-sm text-muted-foreground">Loading...</main>;
  }
  const user = identity.data.user;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    await requestEmailChange(email);
    setMsg("Check the new address for a confirmation link.");
  }

  return (
    <main className="mx-auto flex max-w-xl flex-col gap-6 p-6">
      <h1 className="text-2xl font-semibold">Account</h1>
      <section className="flex flex-col gap-2">
        <div className="text-sm text-muted-foreground">Current email</div>
        <div className="text-sm">{user.email}</div>
        <div className="text-xs text-muted-foreground">{user.emailVerifiedAt ? "Verified" : "Not verified"}</div>
        {!user.emailVerifiedAt ? (
          <button className="w-fit rounded border px-3 py-2 text-sm" onClick={() => resendVerification().then(() => setMsg("Verification email sent."))}>
            Resend verification
          </button>
        ) : null}
      </section>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-sm text-muted-foreground">New email</span>
          <input className="rounded border px-3 py-2" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </label>
        <button className="w-fit rounded bg-foreground px-3 py-2 text-background" type="submit">Request change</button>
      </form>
      {msg ? <p className="text-sm text-muted-foreground">{msg}</p> : null}
    </main>
  );
}

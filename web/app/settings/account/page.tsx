"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { requestEmailChange, resendVerification } from "@/lib/auth/email-flows";
import { useIdentity } from "@/lib/hooks/use-identity";
import { updateProfile, ApiError } from "@/lib/api/client";

export default function AccountSettingsPage() {
  const identity = useIdentity();
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [msg, setMsg] = useState<string | null>(null);

  // Display name state
  const [displayName, setDisplayName] = useState<string | null>(null);
  const [dnMsg, setDnMsg] = useState<string | null>(null);
  const [dnError, setDnError] = useState<string | null>(null);

  if (identity.status !== "authenticated") {
    return <main className="p-6 text-sm text-muted-foreground">Loading...</main>;
  }
  const user = identity.data.user;

  // Initialise displayName from user data on first render.
  const currentDisplayName = displayName ?? user.displayName;

  async function submitDisplayName(e: React.FormEvent) {
    e.preventDefault();
    setDnMsg(null);
    setDnError(null);
    try {
      await updateProfile({ displayName: currentDisplayName });
      await qc.invalidateQueries({ queryKey: ["me"] });
      setDnMsg("Saved.");
    } catch (err) {
      if (err instanceof ApiError) {
        setDnError(err.body?.error ?? err.message);
      } else {
        setDnError("Something went wrong.");
      }
    }
  }

  async function submitEmail(e: React.FormEvent) {
    e.preventDefault();
    await requestEmailChange(email);
    setMsg("Check the new address for a confirmation link.");
  }

  const dnUnchanged = currentDisplayName === user.displayName;
  const dnEmpty = currentDisplayName.trim().length === 0;

  return (
    <main className="mx-auto flex max-w-xl flex-col gap-6 p-6">
      <h1 className="text-2xl font-semibold">Account</h1>

      <form onSubmit={submitDisplayName} className="flex flex-col gap-3">
        <h2 className="text-sm font-medium">Display name</h2>
        <label className="flex flex-col gap-1">
          <input
            className="rounded border px-3 py-2 text-sm"
            type="text"
            value={currentDisplayName}
            onChange={(e) => {
              setDisplayName(e.target.value);
              setDnMsg(null);
              setDnError(null);
            }}
          />
        </label>
        <button
          className="w-fit rounded bg-foreground px-3 py-2 text-sm text-background disabled:opacity-40"
          type="submit"
          disabled={dnUnchanged || dnEmpty}
        >
          Save
        </button>
        {dnMsg ? <p className="text-sm text-muted-foreground">{dnMsg}</p> : null}
        {dnError ? <p className="text-sm text-destructive">{dnError}</p> : null}
      </form>

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
      <form onSubmit={submitEmail} className="flex flex-col gap-3">
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

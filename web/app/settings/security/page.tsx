"use client";

import { useState } from "react";
import { startRegistration } from "@simplewebauthn/browser";
import { KeyRound, RefreshCcw, ShieldCheck, Smartphone } from "lucide-react";
import Image from "next/image";
import {
  beginPasskeyEnrollment,
  completePasskeyEnrollment,
  confirmTOTP,
  enrollTOTP,
  fetchMFAStatus,
  regenerateRecoveryCodes,
  type TOTPSetup,
} from "@/lib/api/client";
import { useQuery, useQueryClient } from "@tanstack/react-query";

export default function SecuritySettingsPage() {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["mfa-status"], queryFn: fetchMFAStatus });
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [message, setMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function startTOTP() {
    setBusy(true);
    setMessage(null);
    try {
      setSetup(await enrollTOTP());
    } finally {
      setBusy(false);
    }
  }

  async function finishTOTP(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage(null);
    try {
      const out = await confirmTOTP(code);
      setRecoveryCodes(out.recoveryCodes);
      setSetup(null);
      setCode("");
      await qc.invalidateQueries({ queryKey: ["mfa-status"] });
    } catch {
      setMessage("That code did not verify.");
    } finally {
      setBusy(false);
    }
  }

  async function addPasskey() {
    setBusy(true);
    setMessage(null);
    try {
      const { options, session } = await beginPasskeyEnrollment();
      const credential = await startRegistration({ optionsJSON: options as never });
      await completePasskeyEnrollment(session, credential);
      await qc.invalidateQueries({ queryKey: ["mfa-status"] });
      setMessage("Passkey added.");
    } catch {
      setMessage("Passkey enrollment was cancelled or failed.");
    } finally {
      setBusy(false);
    }
  }

  async function rotateRecoveryCodes() {
    setBusy(true);
    setMessage(null);
    try {
      const out = await regenerateRecoveryCodes();
      setRecoveryCodes(out.recoveryCodes);
      await qc.invalidateQueries({ queryKey: ["mfa-status"] });
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
      <header className="flex items-center gap-3">
        <ShieldCheck className="size-6" aria-hidden="true" />
        <h1 className="text-2xl font-semibold">Security</h1>
      </header>

      <section className="rounded-lg border bg-card p-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="font-medium">Authenticator app</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              {data?.totpEnrolled ? "Enabled" : "Not enabled"}
            </p>
          </div>
          <button className="inline-flex items-center gap-2 rounded border px-3 py-2 text-sm" onClick={startTOTP} disabled={busy}>
            <Smartphone className="size-4" aria-hidden="true" />
            Set up
          </button>
        </div>
        {setup ? (
          <form onSubmit={finishTOTP} className="mt-4 flex flex-col gap-3 border-t pt-4">
            <Image
              className="size-40 rounded border"
              alt="Authenticator QR code"
              src={`data:image/png;base64,${setup.qrCodeBase64}`}
              width={160}
              height={160}
              unoptimized
            />
            <code className="break-all rounded bg-muted px-2 py-1 text-xs">{setup.secret}</code>
            <input className="max-w-48 rounded border px-3 py-2" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" required />
            <button className="w-fit rounded bg-foreground px-3 py-2 text-background" disabled={busy}>Confirm</button>
          </form>
        ) : null}
      </section>

      <section className="rounded-lg border bg-card p-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="font-medium">Passkeys</h2>
            <p className="mt-1 text-sm text-muted-foreground">{data?.passkeyCount ?? 0} registered</p>
          </div>
          <button className="inline-flex items-center gap-2 rounded border px-3 py-2 text-sm" onClick={addPasskey} disabled={busy}>
            <KeyRound className="size-4" aria-hidden="true" />
            Add
          </button>
        </div>
      </section>

      <section className="rounded-lg border bg-card p-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="font-medium">Recovery codes</h2>
            <p className="mt-1 text-sm text-muted-foreground">{data?.remainingRecoveryCodes ?? 0} remaining</p>
          </div>
          <button className="inline-flex items-center gap-2 rounded border px-3 py-2 text-sm" onClick={rotateRecoveryCodes} disabled={busy}>
            <RefreshCcw className="size-4" aria-hidden="true" />
            Regenerate
          </button>
        </div>
        {recoveryCodes.length ? (
          <div className="mt-4 grid gap-2 border-t pt-4 sm:grid-cols-2">
            {recoveryCodes.map((item) => (
              <code className="rounded bg-muted px-2 py-1 text-sm" key={item}>{item}</code>
            ))}
          </div>
        ) : null}
      </section>
      {message ? <p className="text-sm text-muted-foreground">{message}</p> : null}
    </main>
  );
}

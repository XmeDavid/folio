"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useQueryClient } from "@tanstack/react-query";
import { startAuthentication } from "@simplewebauthn/browser";
import type { Me } from "@/lib/hooks/use-identity";

export default function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [challengeId, setChallengeId] = useState<string | null>(null);
  const [mfaCode, setMfaCode] = useState("");
  const [mfaMode, setMfaMode] = useState<"totp" | "recovery">("totp");
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
      const body = (await res.json()) as { mfaRequired?: boolean; challengeId?: string };
      if (body.mfaRequired && body.challengeId) {
        setChallengeId(body.challengeId);
        return;
      }
      await finishLogin();
    } catch (caught) {
      setErr((caught as Error).message ?? "Login failed");
    } finally {
      setBusy(false);
    }
  }

  async function finishLogin() {
      await qc.invalidateQueries({ queryKey: ["me"] });
      // Re-fetch /me to decide where to land.
      const meRes = await fetch("/api/v1/me", {
        credentials: "include",
        headers: { "X-Folio-Request": "1" },
      });
      if (meRes.ok) {
        const me = (await meRes.json()) as Me;
        // Prefer the user's last-used workspace if it's still in their list;
        // otherwise fall back to the first membership, then to /workspaces.
        const last = me.user?.lastWorkspaceId
          ? me.workspaces.find((w) => w.id === me.user.lastWorkspaceId)
          : null;
        const target = last ?? me.workspaces?.[0];
        router.push((target ? `/w/${target.slug}` : "/workspaces") as Route);
      } else {
        router.push("/workspaces" as Route);
      }
  }

  async function submitMFA(e: React.FormEvent) {
    e.preventDefault();
    if (!challengeId) return;
    setBusy(true);
    setErr(null);
    try {
      const res = await fetch(`/api/v1/auth/login/mfa/${mfaMode}`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify({ challengeId, code: mfaCode }),
      });
      if (!res.ok) {
        setErr(
          "Verification failed. If your network changed since you signed in, please go back and sign in again.",
        );
        return;
      }
      await finishLogin();
    } catch (caught) {
      setErr((caught as Error).message ?? "Login failed");
    } finally {
      setBusy(false);
    }
  }

  async function usePasskey() {
    if (!challengeId) return;
    setBusy(true);
    setErr(null);
    try {
      const begin = await fetch("/api/v1/auth/login/mfa/webauthn/begin", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify({ challengeId }),
      });
      if (!begin.ok) {
        setErr("No passkey challenge is available.");
        return;
      }
      const { options } = (await begin.json()) as { options: unknown };
      const assertion = await startAuthentication({ optionsJSON: options as never });
      const complete = await fetch(`/api/v1/auth/login/mfa/webauthn/complete?challengeId=${encodeURIComponent(challengeId)}`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Folio-Request": "1" },
        body: JSON.stringify(assertion),
      });
      if (!complete.ok) {
        setErr(
          "Passkey verification failed. If your network changed since you signed in, please go back and sign in again.",
        );
        return;
      }
      await finishLogin();
    } catch {
      setErr("Passkey verification was cancelled or failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-6 p-6">
      <h1 className="text-2xl font-semibold">Sign in to Folio</h1>
      {!challengeId ? <form onSubmit={submit} className="flex flex-col gap-3">
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
      </form> : (
        <form onSubmit={submitMFA} className="flex flex-col gap-3">
          <div className="grid grid-cols-2 rounded border p-1 text-sm">
            <button type="button" className={mfaMode === "totp" ? "rounded bg-muted px-3 py-2" : "px-3 py-2"} onClick={() => setMfaMode("totp")}>Authenticator</button>
            <button type="button" className={mfaMode === "recovery" ? "rounded bg-muted px-3 py-2" : "px-3 py-2"} onClick={() => setMfaMode("recovery")}>Recovery</button>
          </div>
          <label className="flex flex-col gap-1">
            <span className="text-sm text-muted-foreground">{mfaMode === "totp" ? "Code" : "Recovery code"}</span>
            <input className="rounded border px-3 py-2" value={mfaCode} onChange={(e) => setMfaCode(e.target.value)} autoComplete="one-time-code" required />
          </label>
          {err ? <p className="text-sm text-red-600">{err}</p> : null}
          <button type="submit" disabled={busy} className="rounded bg-foreground px-3 py-2 text-background">
            {busy ? "Verifying..." : "Verify"}
          </button>
          <button type="button" disabled={busy} onClick={usePasskey} className="rounded border px-3 py-2">
            Use passkey
          </button>
        </form>
      )}
      <p className="text-sm text-muted-foreground">
        New here?{" "}
        <a href="/signup" className="underline">
          Create an account
        </a>
      </p>
    </main>
  );
}

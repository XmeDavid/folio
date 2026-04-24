"use client";

import * as React from "react";
import { useIdentity } from "@/lib/hooks/use-identity";
import { AppShell } from "./shell";

// Gate keeps it simple: no session -> placeholder sign-in prompt; authenticated
// -> normal shell. Once the /signup and /login routes land in §13 this gate
// will redirect to them rather than rendering the placeholder inline.
export function TenantGate({ children }: { children: React.ReactNode }) {
  const identity = useIdentity();

  if (identity.status === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center text-[13px] text-[--color-fg-faint]">
        Loading Folio...
      </div>
    );
  }

  if (identity.status === "unauthenticated") {
    return (
      <div className="mx-auto flex min-h-screen w-full max-w-md flex-col items-center justify-center gap-3 px-6 text-center">
        <div
          aria-hidden
          className="h-6 w-6 rounded-[6px] bg-[--color-accent]"
        />
        <h1 className="text-[20px] leading-tight font-normal tracking-tight">
          Sign in to Folio
        </h1>
        <p className="text-[13px] text-[--color-fg-muted]">
          Authentication UI lives at /signup and /login. Create an account or
          sign in to continue.
        </p>
      </div>
    );
  }

  return <AppShell>{children}</AppShell>;
}

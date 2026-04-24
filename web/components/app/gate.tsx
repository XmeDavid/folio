"use client";

import * as React from "react";
import { useIdentity } from "@/lib/hooks/use-identity";
import { BootstrapForm } from "@/components/onboarding/bootstrap-form";
import { AppShell } from "./shell";

// Gate keeps it simple: no tenant -> onboarding screen; tenant -> normal shell.
// When real auth lands, this becomes a server-driven redirect instead.
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
    return <BootstrapForm />;
  }

  return <AppShell>{children}</AppShell>;
}

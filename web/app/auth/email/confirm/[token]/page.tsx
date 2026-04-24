"use client";

import { use, useEffect, useState } from "react";
import Link from "next/link";
import { confirmEmailChange } from "@/lib/auth/email-flows";

export default function ConfirmEmailChangePage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params);
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading");

  useEffect(() => {
    confirmEmailChange(token).then(() => setStatus("ok")).catch(() => setStatus("error"));
  }, [token]);

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-4 p-6">
      <h1 className="text-2xl font-semibold">Email change</h1>
      <p className="text-sm text-muted-foreground">
        {status === "loading" && "Confirming your new email..."}
        {status === "ok" && "Your account email was changed."}
        {status === "error" && "This confirmation link is invalid or expired."}
      </p>
      <Link href="/settings/account" className="text-sm underline">Go to account settings</Link>
    </main>
  );
}

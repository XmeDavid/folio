"use client";

import { use, useEffect, useState } from "react";
import Link from "next/link";
import { verifyEmail } from "@/lib/auth/email-flows";

export default function VerifyEmailPage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = use(params);
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading");

  useEffect(() => {
    verifyEmail(token).then(() => setStatus("ok")).catch(() => setStatus("error"));
  }, [token]);

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-4 p-6">
      <h1 className="text-2xl font-semibold">Email verification</h1>
      <p className="text-sm text-muted-foreground">
        {status === "loading" && "Verifying your email..."}
        {status === "ok" && "Your email is verified."}
        {status === "error" && "This verification link is invalid or expired."}
      </p>
      <Link href="/login" className="text-sm underline">Go to sign in</Link>
    </main>
  );
}

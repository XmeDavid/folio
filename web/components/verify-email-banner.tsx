"use client";

import { useState } from "react";
import { resendVerification } from "@/lib/auth/email-flows";
import type { MeUser } from "@/lib/hooks/use-identity";

export function VerifyEmailBanner({ user }: { user: MeUser }) {
  const [sent, setSent] = useState(false);
  if (user.emailVerifiedAt) return null;
  return (
    <div className="border-b bg-surface-subtle px-4 py-3 text-sm">
      <div className="mx-auto flex max-w-5xl items-center justify-between gap-3">
        <span>Verify {user.email} to unlock email-protected actions.</span>
        <button
          className="rounded border px-3 py-1 text-xs"
          disabled={sent}
          onClick={async () => {
            await resendVerification();
            setSent(true);
          }}
        >
          {sent ? "Sent" : "Resend"}
        </button>
      </div>
    </div>
  );
}

"use client";

import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { FormError } from "@/components/ui/form-error";
import { Input } from "@/components/ui/input";
import {
  useCreateAdminInvite,
  type PlatformInviteCreated,
} from "@/lib/admin/client";
import { ApiError } from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";

type Stage = "form" | "success";

export function InviteUserDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  if (!open) return null;
  // Mounted only while open; unmount-on-close gives us a fresh state machine
  // for every re-open without needing setState-in-effect resets.
  return <InviteUserDialogContent onClose={onClose} />;
}

function InviteUserDialogContent({ onClose }: { onClose: () => void }) {
  const [email, setEmail] = useState("");
  const [stage, setStage] = useState<Stage>("form");
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<PlatformInviteCreated | null>(null);
  const [copied, setCopied] = useState(false);
  const emailInputRef = useRef<HTMLInputElement>(null);
  const linkInputRef = useRef<HTMLInputElement>(null);

  const create = useCreateAdminInvite();

  // Autofocus the email input once mounted.
  useEffect(() => {
    emailInputRef.current?.focus();
  }, []);

  // Reset the "Copied" feedback after 2s.
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    create.mutate(
      { email: email.trim() === "" ? undefined : email.trim() },
      {
        onSuccess: (data) => {
          setCreated(data);
          setStage("success");
        },
        onError: (err) => {
          setError(formatError(err));
        },
      }
    );
  };

  const handleCopy = async () => {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.acceptUrl);
      setCopied(true);
    } catch {
      // Fallback: select the text in the input so the user can copy manually.
      linkInputRef.current?.select();
    }
  };

  const closeAndReset = () => {
    if (create.isPending) return;
    onClose();
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="invite-user-dialog-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={closeAndReset}
    >
      <div
        className="w-full max-w-md rounded-[16px] border border-border bg-surface p-6 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h3
          id="invite-user-dialog-title"
          className="text-[15px] font-semibold text-fg"
        >
          {stage === "form" ? "Invite user" : "Invite created"}
        </h3>

        {stage === "form" ? (
          <form onSubmit={submit} className="mt-4 flex flex-col gap-4">
            <Field
              label="Email (optional)"
              htmlFor="invite-user-email"
              hint="Leave blank to create an open invite that anyone with the link can accept."
            >
              <Input
                id="invite-user-email"
                ref={emailInputRef}
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="name@example.com"
                autoComplete="off"
              />
            </Field>

            {error ? <FormError>{error}</FormError> : null}

            <div className="mt-1 flex justify-end gap-2">
              <Button
                type="button"
                variant="secondary"
                onClick={closeAndReset}
                disabled={create.isPending}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending}>
                {create.isPending ? "Creating…" : "Create invite"}
              </Button>
            </div>
          </form>
        ) : (
          <div className="mt-4 flex flex-col gap-4">
            <Field
              label="Accept link"
              htmlFor="invite-user-accept-url"
              hint="This link is shown only once. Send it to the user by your preferred channel — Folio's mailer may not be configured."
            >
              <div className="flex gap-2">
                <Input
                  id="invite-user-accept-url"
                  ref={linkInputRef}
                  readOnly
                  value={created?.acceptUrl ?? ""}
                  onFocus={(e) => e.currentTarget.select()}
                  className="font-mono text-[12px]"
                />
                <Button
                  type="button"
                  variant="secondary"
                  onClick={handleCopy}
                  className="shrink-0"
                >
                  {copied ? "Copied" : "Copy link"}
                </Button>
              </div>
            </Field>

            <div className="mt-1 flex justify-end">
              <Button type="button" onClick={closeAndReset}>
                Done
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    return friendlyError(err.body?.code, err.body?.error ?? err.message);
  }
  return (err as Error)?.message ?? "Request failed";
}

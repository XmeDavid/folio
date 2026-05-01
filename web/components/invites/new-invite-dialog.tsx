"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field } from "@/components/ui/field";
import { FormError } from "@/components/ui/form-error";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  ApiError,
  createInvite,
  type MemberRole,
  type WorkspaceInviteCreated,
} from "@/lib/api/client";
import { friendlyError } from "@/lib/api/errors";

type Stage = "form" | "success";

export function NewInviteDialog({
  open,
  onClose,
  workspaceId,
  canInviteOwners,
}: {
  open: boolean;
  onClose: () => void;
  workspaceId: string;
  canInviteOwners: boolean;
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      {/* Mount the body only while open so internal state machine resets
          on each re-open without setState-in-effect resets. */}
      {open ? (
        <NewInviteDialogContent
          onClose={onClose}
          workspaceId={workspaceId}
          canInviteOwners={canInviteOwners}
        />
      ) : null}
    </Dialog>
  );
}

function NewInviteDialogContent({
  onClose,
  workspaceId,
  canInviteOwners,
}: {
  onClose: () => void;
  workspaceId: string;
  canInviteOwners: boolean;
}) {
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<MemberRole>("member");
  const [stage, setStage] = useState<Stage>("form");
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<WorkspaceInviteCreated | null>(null);
  const [copied, setCopied] = useState(false);
  const linkInputRef = useRef<HTMLInputElement>(null);

  const create = useMutation({
    mutationFn: () => createInvite(workspaceId, { email, role }),
    onSuccess: async (data) => {
      await qc.invalidateQueries({ queryKey: ["members", workspaceId] });
      setError(null);
      setCreated(data);
      setStage("success");
    },
    onError: (err) => {
      setError(formatError(err));
    },
  });

  // Reset the "Copied" feedback after 2s.
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    create.mutate();
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

  return (
    <DialogContent
      // Block dismissal while the request is in flight so the
      // single-shown invite token isn't lost mid-creation.
      onInteractOutside={(event) => {
        if (create.isPending) event.preventDefault();
      }}
      onEscapeKeyDown={(event) => {
        if (create.isPending) event.preventDefault();
      }}
    >
      <DialogHeader>
        <DialogTitle>
          {stage === "form" ? "New invite" : "Invite created"}
        </DialogTitle>
      </DialogHeader>

      {stage === "form" ? (
        <form onSubmit={submit} className="mt-4 flex flex-col gap-4">
          <Field label="Email" htmlFor="new-invite-email">
            <Input
              id="new-invite-email"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="name@example.com"
              autoComplete="off"
              autoFocus
            />
          </Field>

          <Field
            label="Role"
            htmlFor="new-invite-role"
            hint={
              !canInviteOwners
                ? "Only owners can invite other owners."
                : undefined
            }
          >
            <Select
              id="new-invite-role"
              value={role}
              onChange={(e) => setRole(e.target.value as MemberRole)}
            >
              <option value="member">member</option>
              {canInviteOwners ? <option value="owner">owner</option> : null}
            </Select>
          </Field>

          {error ? <FormError>{error}</FormError> : null}

          <div className="mt-1 flex justify-end gap-2">
            <Button
              type="button"
              variant="secondary"
              onClick={onClose}
              disabled={create.isPending}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? "Sending…" : "Send invite"}
            </Button>
          </div>
        </form>
      ) : (
        <div className="mt-4 flex flex-col gap-4">
          <Field
            label="Accept link"
            htmlFor="new-invite-accept-url"
            hint="Send this link to the invitee. We've also emailed it to them, but the email may not arrive if the mailer isn't configured."
          >
            <div className="flex gap-2">
              <Input
                id="new-invite-accept-url"
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
            <Button type="button" onClick={onClose}>
              Done
            </Button>
          </div>
        </div>
      )}
    </DialogContent>
  );
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    return friendlyError(err.body?.code, err.body?.error ?? err.message);
  }
  return (err as Error)?.message ?? "Request failed";
}

"use client";

import * as React from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArchiveRestore,
  GitMerge,
  Pencil,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { MerchantForm } from "@/components/classification/merchants-table";
import { MerchantMergeDialog } from "@/components/classification/merchant-merge-dialog";
import {
  archiveMerchant,
  updateMerchant,
  type Category,
  type Merchant,
} from "@/lib/api/client";

export function MerchantDetailSidebar({
  workspaceId,
  workspaceSlug,
  merchant,
  categoryById,
  leafCategories,
  transactionCount,
}: {
  workspaceId: string;
  workspaceSlug: string;
  merchant: Merchant;
  categoryById: Map<string, Category>;
  leafCategories: Category[];
  transactionCount: number;
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = React.useState(false);
  const [mergeOpen, setMergeOpen] = React.useState(false);

  const archiveMutation = useMutation({
    mutationFn: async () => {
      if (merchant.archivedAt) {
        await updateMerchant(workspaceId, merchant.id, { archived: false });
      } else {
        await archiveMerchant(workspaceId, merchant.id);
      }
    },
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["merchant", workspaceId, merchant.id],
        }),
        queryClient.invalidateQueries({
          queryKey: ["merchants", workspaceId],
        }),
      ]);
    },
  });

  const defaultCategory = merchant.defaultCategoryId
    ? categoryById.get(merchant.defaultCategoryId)
    : null;

  return (
    <Card className="flex flex-col gap-4 p-5">
      <div className="flex items-center gap-3">
        <MerchantAvatar logoUrl={merchant.logoUrl} />
        <div className="min-w-0">
          <div className="truncate text-[15px] font-medium text-fg">
            {merchant.canonicalName}
          </div>
          <div className="mt-1">
            {merchant.archivedAt ? (
              <Badge variant="neutral">Archived</Badge>
            ) : (
              <Badge variant="success">Active</Badge>
            )}
          </div>
        </div>
      </div>

      {editing ? (
        <div className="border-t border-border pt-4">
          <MerchantForm
            slug={workspaceSlug}
            workspaceId={workspaceId}
            leafCategories={leafCategories}
            merchant={merchant}
            transactionCount={transactionCount}
            onDone={() => setEditing(false)}
            onCancel={() => setEditing(false)}
          />
        </div>
      ) : (
        <dl className="flex flex-col gap-3 border-t border-border pt-4 text-[13px]">
          <SidebarField label="Default category">
            {defaultCategory ? (
              <span className="text-fg">{defaultCategory.name}</span>
            ) : (
              <span className="text-fg-faint">— none —</span>
            )}
          </SidebarField>
          <SidebarField label="Industry">
            {merchant.industry ? (
              <span className="text-fg">{merchant.industry}</span>
            ) : (
              <span className="text-fg-faint">—</span>
            )}
          </SidebarField>
          <SidebarField label="Website">
            {merchant.website ? (
              <a
                href={merchant.website}
                target="_blank"
                rel="noreferrer"
                className="text-fg hover:underline"
              >
                {merchant.website}
              </a>
            ) : (
              <span className="text-fg-faint">—</span>
            )}
          </SidebarField>
          <SidebarField label="Notes">
            {merchant.notes ? (
              <span className="whitespace-pre-line text-fg">
                {merchant.notes}
              </span>
            ) : (
              <span className="text-fg-faint">—</span>
            )}
          </SidebarField>
        </dl>
      )}

      {!editing ? (
        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setEditing(true)}
          >
            <Pencil className="h-3.5 w-3.5" />
            Edit details
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setMergeOpen(true)}
            disabled={!!merchant.archivedAt}
            title={
              merchant.archivedAt
                ? "Restore this merchant before merging."
                : undefined
            }
          >
            <GitMerge className="h-3.5 w-3.5" />
            Merge into…
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={archiveMutation.isPending}
            onClick={() => archiveMutation.mutate()}
          >
            {merchant.archivedAt ? (
              <>
                <ArchiveRestore className="h-3.5 w-3.5" />
                Restore
              </>
            ) : (
              <>
                <Archive className="h-3.5 w-3.5" />
                Archive
              </>
            )}
          </Button>
        </div>
      ) : null}
      <MerchantMergeDialog
        open={mergeOpen}
        workspaceId={workspaceId}
        workspaceSlug={workspaceSlug}
        source={merchant}
        onClose={() => setMergeOpen(false)}
      />
    </Card>
  );
}

function SidebarField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-[11px] font-medium tracking-[0.07em] text-fg-faint uppercase">
        {label}
      </dt>
      <dd className="text-[13px] leading-snug">{children}</dd>
    </div>
  );
}

function MerchantAvatar({ logoUrl }: { logoUrl?: string | null }) {
  if (logoUrl) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={logoUrl}
        alt=""
        className="h-10 w-10 shrink-0 rounded border border-border bg-surface object-cover"
      />
    );
  }
  return (
    <div
      aria-hidden
      className="h-10 w-10 shrink-0 rounded border border-border bg-surface-subtle"
    />
  );
}

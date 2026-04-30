"use client";

import { use } from "react";
import { TransfersReviewQueue } from "@/components/transfers/transfers-review-queue";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { PageHeader } from "@/components/app/page-header";

export default function TransfersReviewPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  if (!workspace) return null;
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Transfers"
        title="Review proposed transfers"
        description="Suggested cross-account pairs awaiting confirmation."
      />
      <TransfersReviewQueue workspaceId={workspace.id} mode="page" />
    </div>
  );
}

"use client";

import { use } from "react";
import { ClassificationPlaceholderPage } from "@/components/classification/placeholder-page";

export default function MerchantsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  return (
    <ClassificationPlaceholderPage
      eyebrow="Classification"
      title="Merchants"
      description="Clean up counterparties into canonical merchants with default categories."
      slug={slug}
    />
  );
}

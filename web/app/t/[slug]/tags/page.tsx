"use client";

import { use } from "react";
import { ClassificationPlaceholderPage } from "@/components/classification/placeholder-page";

export default function TagsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  return (
    <ClassificationPlaceholderPage
      eyebrow="Classification"
      title="Tags"
      description="Maintain flat labels for reimbursable, tax, trips, and other cross-cutting slices."
      slug={slug}
    />
  );
}

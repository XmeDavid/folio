"use client";

import { use } from "react";
import { ClassificationPlaceholderPage } from "@/components/classification/placeholder-page";

export default function RulesPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  return (
    <ClassificationPlaceholderPage
      eyebrow="Classification"
      title="Rules"
      description="Create ordered rules that classify incoming transactions without raw JSON."
      slug={slug}
    />
  );
}

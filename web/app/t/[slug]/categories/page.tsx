"use client";

import { use } from "react";
import { ClassificationPlaceholderPage } from "@/components/classification/placeholder-page";

export default function CategoriesPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  return (
    <ClassificationPlaceholderPage
      eyebrow="Classification"
      title="Categories"
      description="Build a readable category tree for spending, income, and planning rollups."
      slug={slug}
    />
  );
}

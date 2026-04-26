import Link from "next/link";
import type { Route } from "next";
import { ArrowRight } from "lucide-react";
import { EmptyState } from "@/components/app/empty";
import { PageHeader } from "@/components/app/page-header";
import { Button } from "@/components/ui/button";

export function ClassificationPlaceholderPage({
  eyebrow,
  title,
  description,
  slug,
}: {
  eyebrow: string;
  title: string;
  description: string;
  slug: string;
}) {
  return (
    <div className="flex flex-col gap-8">
      <PageHeader eyebrow={eyebrow} title={title} description={description} />
      <EmptyState
        title={`${title} management is next`}
        description="The backend foundation exists. This page is wired into the workspace shell so the next slice can add the list and editing workflow in place."
        action={
          <Button asChild variant="secondary">
            <Link href={`/w/${slug}/transactions` as Route}>
              Open transactions
              <ArrowRight className="h-4 w-4" />
            </Link>
          </Button>
        }
      />
    </div>
  );
}

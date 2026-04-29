"use client";

import * as React from "react";
import { use } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  MerchantForm,
  MerchantsTable,
} from "@/components/classification/merchants-table";
import {
  fetchCategories,
  fetchMerchants,
  type Category,
} from "@/lib/api/client";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";

export default function MerchantsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const [creating, setCreating] = React.useState(false);
  const [search, setSearch] = React.useState("");
  const [includeArchived, setIncludeArchived] = React.useState(false);

  const merchantsQuery = useQuery({
    queryKey: ["merchants", workspaceId, includeArchived],
    queryFn: () => fetchMerchants(workspaceId!, { includeArchived }),
    enabled: !!workspaceId,
  });

  // Fetch categories with archived included so we can resolve names for
  // merchants whose default points at an archived category.
  const categoriesQuery = useQuery({
    queryKey: ["categories", workspaceId, true],
    queryFn: () => fetchCategories(workspaceId!, { includeArchived: true }),
    enabled: !!workspaceId,
  });

  if (!workspace) return null;

  const merchants = merchantsQuery.data ?? [];
  const categories = categoriesQuery.data ?? [];
  const categoryById = new Map(categories.map((c) => [c.id, c]));
  const leafCategories = computeLeafCategories(categories);

  const term = search.toLowerCase().trim();
  const filtered = term
    ? merchants.filter((m) => m.canonicalName.toLowerCase().includes(term))
    : merchants;
  const sorted = [...filtered].sort((a, b) =>
    a.canonicalName.localeCompare(b.canonicalName)
  );

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Classification"
        title="Merchants"
        description="Clean up counterparties into canonical merchants with default categories. Click a merchant to see its transactions and aliases."
        actions={
          <Button onClick={() => setCreating((v) => !v)}>
            <Plus className="h-4 w-4" />
            {creating ? "Close" : "Add merchant"}
          </Button>
        }
      />

      {creating && workspaceId ? (
        <Card>
          <CardHeader>
            <CardTitle>New merchant</CardTitle>
          </CardHeader>
          <CardContent>
            <MerchantForm
              workspaceId={workspaceId}
              leafCategories={leafCategories}
              onDone={() => setCreating(false)}
              onCancel={() => setCreating(false)}
            />
          </CardContent>
        </Card>
      ) : null}

      {merchantsQuery.isError ? (
        <ErrorBanner
          title="Couldn't load merchants"
          description="Check that the backend is running and your session is still valid."
        />
      ) : null}

      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <Input
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Search merchants..."
          className="md:max-w-sm"
        />
        <label className="flex items-center gap-2 text-[12px] text-fg-muted">
          <input
            type="checkbox"
            className="h-3.5 w-3.5"
            checked={includeArchived}
            onChange={(event) => setIncludeArchived(event.target.checked)}
          />
          Show archived
        </label>
      </div>

      {merchantsQuery.isLoading ? (
        <LoadingText />
      ) : sorted.length > 0 ? (
        <MerchantsTable
          slug={slug}
          workspaceId={workspace.id}
          merchants={sorted}
          categoryById={categoryById}
          leafCategories={leafCategories}
        />
      ) : (
        <EmptyState
          title={merchants.length === 0 ? "No merchants yet" : "No merchants match"}
          description={
            merchants.length === 0
              ? "Merchants are auto-created when you import transactions. Add one manually if you want to set defaults ahead of time."
              : "Try a different search term, or toggle 'Show archived'."
          }
          action={
            merchants.length === 0 ? (
              <Button onClick={() => setCreating(true)}>
                <Plus className="h-4 w-4" />
                Add merchant
              </Button>
            ) : null
          }
        />
      )}
    </div>
  );
}

function computeLeafCategories(categories: Category[]): Category[] {
  const parentIds = new Set<string>();
  for (const category of categories) {
    if (category.parentId) parentIds.add(category.parentId);
  }
  return categories
    .filter((category) => !parentIds.has(category.id))
    .filter((category) => !category.archivedAt)
    .sort((a, b) => a.name.localeCompare(b.name));
}

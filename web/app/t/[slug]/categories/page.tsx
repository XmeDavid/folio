"use client";

import * as React from "react";
import { use } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ArchiveRestore, Check, Pencil, Plus, X } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { FormError } from "@/components/ui/form-error";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  ApiError,
  archiveCategory,
  createCategory,
  fetchCategories,
  updateCategory,
  type Category,
} from "@/lib/api/client";
import { useCurrentTenant } from "@/lib/hooks/use-identity";

export default function CategoriesPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const tenant = useCurrentTenant(slug);
  const tenantId = tenant?.id ?? null;
  const [creating, setCreating] = React.useState(false);
  const [includeArchived, setIncludeArchived] = React.useState(false);

  const categoriesQuery = useQuery({
    queryKey: ["categories", tenantId, includeArchived],
    queryFn: () => fetchCategories(tenantId!, { includeArchived }),
    enabled: !!tenantId,
  });

  if (!tenant) return null;

  const categories = categoriesQuery.data ?? [];
  const activeCategories = categories.filter(
    (category) => !category.archivedAt
  );

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Classification"
        title="Categories"
        description="Build the category tree used for transaction classification, spending rollups, and budgets."
        actions={
          <Button onClick={() => setCreating((v) => !v)}>
            <Plus className="h-4 w-4" />
            {creating ? "Close" : "Add category"}
          </Button>
        }
      />

      {creating && tenantId ? (
        <Card>
          <CardHeader>
            <CardTitle>New category</CardTitle>
          </CardHeader>
          <CardContent>
            <CategoryForm
              tenantId={tenantId}
              categories={activeCategories}
              onDone={() => setCreating(false)}
              onCancel={() => setCreating(false)}
            />
          </CardContent>
        </Card>
      ) : null}

      {categoriesQuery.isError ? (
        <ErrorBanner
          title="Couldn't load categories"
          description="Check that the backend is running and your session is still valid."
        />
      ) : null}

      {categoriesQuery.isLoading ? (
        <LoadingText />
      ) : categories.length > 0 ? (
        <div className="flex flex-col gap-2">
          <label className="text-fg-muted flex items-center gap-2 self-end text-[12px]">
            <input
              type="checkbox"
              className="h-3.5 w-3.5"
              checked={includeArchived}
              onChange={(event) => setIncludeArchived(event.target.checked)}
            />
            Show archived
          </label>
          <CategoryTree tenantId={tenant.id} categories={categories} />
        </div>
      ) : (
        <EmptyState
          title="No categories yet"
          description="Create parent groups such as Food or Housing, then add leaf categories for transactions."
          action={
            <Button onClick={() => setCreating(true)}>
              <Plus className="h-4 w-4" />
              Add category
            </Button>
          }
        />
      )}
    </div>
  );
}

function CategoryTree({
  tenantId,
  categories,
}: {
  tenantId: string;
  categories: Category[];
}) {
  const roots = categories
    .filter((category) => !category.parentId)
    .sort(sortCategories);
  const childrenByParent = new Map<string, Category[]>();
  for (const category of categories) {
    if (!category.parentId) continue;
    const children = childrenByParent.get(category.parentId) ?? [];
    children.push(category);
    childrenByParent.set(category.parentId, children);
  }
  for (const children of childrenByParent.values()) {
    children.sort(sortCategories);
  }

  return (
    <Card className="overflow-hidden">
      <div className="border-border text-fg-faint hidden grid-cols-[1fr_160px_120px] items-center gap-4 border-b px-5 py-2 text-[11px] font-medium tracking-[0.07em] uppercase md:grid">
        <span>Category</span>
        <span>Status</span>
        <span className="text-right">Actions</span>
      </div>
      <ul className="divide-border divide-y">
        {roots.map((category) => (
          <CategoryRow
            key={category.id}
            tenantId={tenantId}
            category={category}
            categories={categories}
            childrenByParent={childrenByParent}
            depth={0}
          />
        ))}
      </ul>
    </Card>
  );
}

function CategoryRow({
  tenantId,
  category,
  categories,
  childrenByParent,
  depth,
}: {
  tenantId: string;
  category: Category;
  categories: Category[];
  childrenByParent: Map<string, Category[]>;
  depth: number;
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = React.useState(false);
  const children = childrenByParent.get(category.id) ?? [];
  const archiveMutation = useMutation({
    mutationFn: async () => {
      if (category.archivedAt) {
        await updateCategory(tenantId, category.id, { archived: false });
      } else {
        await archiveCategory(tenantId, category.id);
      }
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["categories", tenantId],
      });
      await queryClient.invalidateQueries({
        queryKey: ["transactions", tenantId],
      });
    },
  });

  return (
    <>
      <li className="grid grid-cols-1 gap-3 px-5 py-3 md:grid-cols-[1fr_160px_120px] md:items-center md:gap-4">
        {editing ? (
          <div className="md:col-span-3">
            <CategoryForm
              tenantId={tenantId}
              categories={categories.filter(
                (candidate) => candidate.id !== category.id
              )}
              category={category}
              onDone={() => setEditing(false)}
              onCancel={() => setEditing(false)}
            />
          </div>
        ) : (
          <>
            <div className="min-w-0" style={{ paddingLeft: depth * 18 }}>
              <div className="text-fg truncate text-[14px] font-medium">
                {category.name}
              </div>
              <div className="text-fg-faint text-[12px]">
                {children.length > 0
                  ? `${children.length} child categories`
                  : "Leaf category"}
              </div>
            </div>
            <span>
              {category.archivedAt ? (
                <Badge variant="neutral">Archived</Badge>
              ) : children.length > 0 ? (
                <Badge variant="info">Group</Badge>
              ) : (
                <Badge variant="success">Leaf</Badge>
              )}
            </span>
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                size="icon"
                onClick={() => setEditing(true)}
              >
                <Pencil className="h-4 w-4" />
                <span className="sr-only">Edit category</span>
              </Button>
              <Button
                variant="ghost"
                size="icon"
                disabled={archiveMutation.isPending}
                onClick={() => archiveMutation.mutate()}
              >
                {category.archivedAt ? (
                  <ArchiveRestore className="h-4 w-4" />
                ) : (
                  <Archive className="h-4 w-4" />
                )}
                <span className="sr-only">
                  {category.archivedAt
                    ? "Restore category"
                    : "Archive category"}
                </span>
              </Button>
            </div>
          </>
        )}
      </li>
      {children.map((child) => (
        <CategoryRow
          key={child.id}
          tenantId={tenantId}
          category={child}
          categories={categories}
          childrenByParent={childrenByParent}
          depth={depth + 1}
        />
      ))}
    </>
  );
}

function CategoryForm({
  tenantId,
  categories,
  category,
  onDone,
  onCancel,
}: {
  tenantId: string;
  categories: Category[];
  category?: Category;
  onDone: () => void;
  onCancel: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = React.useState(category?.name ?? "");
  const [parentId, setParentId] = React.useState(category?.parentId ?? "");
  const [sortOrder, setSortOrder] = React.useState(
    String(category?.sortOrder ?? 0)
  );

  const mutation = useMutation({
    mutationFn: () => {
      const body = {
        name: name.trim(),
        parentId: parentId || null,
        sortOrder: Number.parseInt(sortOrder, 10) || 0,
      };
      return category
        ? updateCategory(tenantId, category.id, body)
        : createCategory(tenantId, body);
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["categories", tenantId],
      });
      onDone();
    },
  });
  const error =
    mutation.error instanceof ApiError ? mutation.error.message : null;

  return (
    <form
      className="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(180px,0.65fr)_120px_auto]"
      onSubmit={(event) => {
        event.preventDefault();
        mutation.mutate();
      }}
    >
      <Field label="Name" htmlFor={`category-name-${category?.id ?? "new"}`}>
        <Input
          id={`category-name-${category?.id ?? "new"}`}
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder="Groceries"
        />
      </Field>
      <Field
        label="Parent"
        htmlFor={`category-parent-${category?.id ?? "new"}`}
      >
        <Select
          id={`category-parent-${category?.id ?? "new"}`}
          value={parentId}
          onChange={(event) => setParentId(event.target.value)}
        >
          <option value="">Root category</option>
          {categories
            .filter((candidate) => !candidate.archivedAt)
            .map((candidate) => (
              <option key={candidate.id} value={candidate.id}>
                {candidate.name}
              </option>
            ))}
        </Select>
      </Field>
      <Field label="Order" htmlFor={`category-order-${category?.id ?? "new"}`}>
        <Input
          id={`category-order-${category?.id ?? "new"}`}
          inputMode="numeric"
          value={sortOrder}
          onChange={(event) => setSortOrder(event.target.value)}
        />
      </Field>
      <div className="flex items-end gap-2">
        <Button type="submit" size="icon" disabled={mutation.isPending}>
          <Check className="h-4 w-4" />
          <span className="sr-only">Save category</span>
        </Button>
        <Button
          type="button"
          variant="secondary"
          size="icon"
          onClick={onCancel}
        >
          <X className="h-4 w-4" />
          <span className="sr-only">Cancel</span>
        </Button>
      </div>
      {error ? <FormError className="md:col-span-4">{error}</FormError> : null}
    </form>
  );
}

function sortCategories(a: Category, b: Category): number {
  if (a.sortOrder !== b.sortOrder) return a.sortOrder - b.sortOrder;
  return a.name.localeCompare(b.name);
}

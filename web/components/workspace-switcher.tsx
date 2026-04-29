"use client";

import { useRouter } from "next/navigation";
import type { Route } from "next";
import { Check, ChevronsUpDown, Shield } from "lucide-react";
import { useIdentity } from "@/lib/hooks/use-identity";
import { updateLastWorkspace } from "@/lib/api/client";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export function WorkspaceSwitcher({ currentSlug }: { currentSlug: string }) {
  const id = useIdentity();
  const router = useRouter();

  if (id.status !== "authenticated") return null;

  const { workspaces, user } = id.data;
  const current = workspaces.find((w) => w.slug === currentSlug);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          className={cn(
            "inline-flex h-9 max-w-[52vw] items-center gap-1.5 rounded-[8px] border border-border-strong bg-surface px-2 text-[13px] text-fg",
            "transition-colors duration-100 hover:bg-surface-subtle focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-1"
          )}
        >
          <span className="truncate">{current?.name ?? currentSlug}</span>
          <ChevronsUpDown className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
        </button>
      </DropdownMenuTrigger>

      <DropdownMenuContent align="start">
        {workspaces.map((w) => {
          const isActive = w.slug === currentSlug;
          return (
            <DropdownMenuItem
              key={w.id}
              aria-current={isActive ? "true" : undefined}
              onSelect={() => {
                updateLastWorkspace(w.id).catch(() => {});
                router.push(`/w/${w.slug}` as Route);
              }}
            >
              <Check
                className={cn(
                  "h-3.5 w-3.5 shrink-0",
                  isActive ? "opacity-100" : "opacity-0"
                )}
              />
              <span className="truncate">{w.name}</span>
            </DropdownMenuItem>
          );
        })}

        {user.isAdmin && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={() => {
                router.push("/admin/users" as Route);
              }}
            >
              <Shield className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
              <span>Admin console</span>
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

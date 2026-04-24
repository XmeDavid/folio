"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useIdentity } from "@/lib/hooks/use-identity";

export default function Root() {
  const id = useIdentity();
  const router = useRouter();
  useEffect(() => {
    if (id.status === "unauthenticated") {
      router.replace("/login" as Route);
    }
    if (id.status === "authenticated") {
      const slug = id.data.tenants[0]?.slug;
      router.replace((slug ? `/t/${slug}` : "/tenants") as Route);
    }
  }, [id.status, id.data, router]);
  return (
    <div className="p-6 text-sm text-muted-foreground">Loading…</div>
  );
}

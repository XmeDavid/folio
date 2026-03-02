"use client";

import { PortfolioCacheProvider } from "@/lib/cache-context";
import type { ReactNode } from "react";

export function Providers({ children }: { children: ReactNode }) {
  return <PortfolioCacheProvider>{children}</PortfolioCacheProvider>;
}

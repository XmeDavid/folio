"use client";

import * as React from "react";

export type DossierTabSpec = {
  id: string;
  label: string;
  icon?: React.ReactNode;
  count: number;
  drawerContent: React.ReactNode;
};

type Ctx = {
  tabs: DossierTabSpec[];
  setTab: (spec: DossierTabSpec) => void;
  removeTab: (id: string) => void;
};

const DossierContext = React.createContext<Ctx | null>(null);

export function DossierProvider({ children }: { children: React.ReactNode }) {
  const [tabs, setTabs] = React.useState<DossierTabSpec[]>([]);

  const setTab = React.useCallback((spec: DossierTabSpec) => {
    setTabs((current) => {
      const idx = current.findIndex((t) => t.id === spec.id);
      if (idx === -1) return [...current, spec];
      const next = current.slice();
      next[idx] = spec;
      return next;
    });
  }, []);

  const removeTab = React.useCallback((id: string) => {
    setTabs((current) => current.filter((t) => t.id !== id));
  }, []);

  const value = React.useMemo<Ctx>(
    () => ({ tabs, setTab, removeTab }),
    [tabs, setTab, removeTab],
  );
  return (
    <DossierContext.Provider value={value}>{children}</DossierContext.Provider>
  );
}

function useDossier(): Ctx {
  const ctx = React.useContext(DossierContext);
  if (!ctx) throw new Error("Dossier hooks must be used inside <DossierProvider>");
  return ctx;
}

export function useDossierTabs(): DossierTabSpec[] {
  return useDossier().tabs;
}

/**
 * Register a dossier tab. Pass null to deregister. The tab is hidden when
 * count <= 0 (the registration is removed automatically); pass null to
 * remove the registration entirely (e.g. on unmount).
 */
export function useRegisterDossierTab(spec: DossierTabSpec | null) {
  const ctx = useDossier();
  React.useEffect(() => {
    if (!spec || spec.count <= 0) {
      if (spec) ctx.removeTab(spec.id);
      return;
    }
    ctx.setTab(spec);
    const id = spec.id;
    return () => ctx.removeTab(id);
  }, [ctx, spec]);
}

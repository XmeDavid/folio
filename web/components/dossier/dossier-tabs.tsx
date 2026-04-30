"use client";

import * as React from "react";
import { useDossierTabs } from "./registry";
import { DossierTab } from "./dossier-tab";
import { DossierDrawer } from "./dossier-drawer";

export function DossierTabs() {
  const tabs = useDossierTabs();
  const [openId, setOpenId] = React.useState<string | null>(null);

  if (tabs.length === 0) return null;

  const open = tabs.find((t) => t.id === openId) ?? null;

  return (
    <>
      <div className="fixed top-1/3 right-0 z-30 flex flex-col gap-2">
        {tabs.map((tab) => (
          <DossierTab
            key={tab.id}
            spec={tab}
            onClick={() => setOpenId(tab.id)}
          />
        ))}
      </div>
      {open ? (
        <DossierDrawer title={open.label} onClose={() => setOpenId(null)}>
          {open.drawerContent}
        </DossierDrawer>
      ) : null}
    </>
  );
}

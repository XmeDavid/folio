"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  LayoutDashboard,
  ArrowLeftRight,
  Upload,
  Layers,
  Landmark,
  CreditCard,
  Wallet,
  Building2,
  Store,
  Menu,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";

const navItems = [
  { href: "/overview", label: "Overview", icon: Landmark },
  { href: "/", label: "Investments", icon: LayoutDashboard },
  { href: "/spending", label: "Spending", icon: CreditCard },
  { href: "/banking", label: "Banking", icon: Wallet },
  { href: "/positions", label: "Positions", icon: Layers },
  { href: "/transactions", label: "Trades", icon: ArrowLeftRight },
  { href: "/merchants", label: "Merchants", icon: Store },
  { href: "/import", label: "Import", icon: Upload },
  { href: "/accounts", label: "Accounts", icon: Building2 },
];

function NavContent({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();

  return (
    <>
      <div className="p-5 border-b border-border-subtle">
        <h1 className="text-lg font-semibold tracking-tight text-text-primary">
          folio<span className="text-accent">.</span>
        </h1>
        <p className="text-[11px] text-text-tertiary mt-0.5 font-mono tracking-wider uppercase">
          Finance Tracker
        </p>
      </div>
      <nav className="flex-1 p-3 space-y-0.5">
        {navItems.map((item) => {
          const active =
            item.href === "/"
              ? pathname === "/"
              : pathname.startsWith(item.href);
          return (
            <Link
              key={item.href}
              href={item.href}
              onClick={onNavigate}
              className={cn(
                "flex items-center gap-2.5 px-3 py-2 text-sm rounded-lg transition-colors",
                active
                  ? "bg-bg-hover text-text-primary"
                  : "text-text-secondary hover:bg-bg-hover hover:text-text-primary"
              )}
            >
              <item.icon size={16} className={cn(active ? "opacity-100" : "opacity-60")} />
              {item.label}
            </Link>
          );
        })}
      </nav>
      <div className="p-4 border-t border-border-subtle">
        <p className="text-[10px] text-text-tertiary font-mono">v2.0.0</p>
      </div>
    </>
  );
}

export function Sidebar() {
  const [open, setOpen] = useState(false);

  return (
    <>
      {/* Mobile top bar */}
      <div className="fixed top-0 left-0 right-0 z-40 flex md:hidden items-center gap-3 px-4 py-3 bg-bg-secondary border-b border-border-subtle">
        <button
          onClick={() => setOpen(true)}
          className="p-1 text-text-secondary hover:text-text-primary transition-colors"
          aria-label="Open menu"
        >
          <Menu size={20} />
        </button>
        <h1 className="text-base font-semibold tracking-tight text-text-primary">
          folio<span className="text-accent">.</span>
        </h1>
      </div>

      {/* Desktop sidebar */}
      <aside className="hidden md:flex w-56 shrink-0 border-r border-border-subtle bg-bg-secondary flex-col">
        <NavContent />
      </aside>

      {/* Mobile drawer overlay */}
      {open && (
        <div className="fixed inset-0 z-50 md:hidden">
          {/* Backdrop */}
          <div
            className="absolute inset-0 bg-black/60"
            onClick={() => setOpen(false)}
          />
          {/* Drawer */}
          <aside className="relative w-64 h-full bg-bg-secondary border-r border-border-subtle flex flex-col animate-slide-in">
            <button
              onClick={() => setOpen(false)}
              className="absolute top-4 right-3 p-1 text-text-tertiary hover:text-text-primary transition-colors"
              aria-label="Close menu"
            >
              <X size={18} />
            </button>
            <NavContent onNavigate={() => setOpen(false)} />
          </aside>
        </div>
      )}
    </>
  );
}

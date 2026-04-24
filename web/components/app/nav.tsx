"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  LayoutDashboard,
  Wallet,
  ArrowLeftRight,
  Tags,
  Target,
  CalendarDays,
  PiggyBank,
  LineChart,
  Search,
  Plane,
  BookOpen,
  Settings,
} from "lucide-react";
import { cn } from "@/lib/utils";

type Item = {
  href?: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  disabled?: boolean;
};

type Group = {
  label: string;
  items: Item[];
};

// Groups are modelled after the Feature Bible long-term IA. Only the routes
// that exist today are enabled; the rest are explicitly marked as planned so
// sequencing intent is visible in the shell instead of being hidden.
const groups: Group[] = [
  {
    label: "Overview",
    items: [
      { href: "/", label: "Dashboard", icon: LayoutDashboard },
      { label: "Net worth", icon: LineChart, disabled: true },
      { label: "Calendar", icon: CalendarDays, disabled: true },
    ],
  },
  {
    label: "Ledger",
    items: [
      { href: "/accounts", label: "Accounts", icon: Wallet },
      { href: "/transactions", label: "Transactions", icon: ArrowLeftRight },
      { label: "Categories", icon: Tags, disabled: true },
      { label: "Search", icon: Search, disabled: true },
    ],
  },
  {
    label: "Planning",
    items: [
      { label: "Budgets & cycles", icon: PiggyBank, disabled: true },
      { label: "Goals & savings", icon: Target, disabled: true },
      { label: "Trips", icon: Plane, disabled: true },
    ],
  },
  {
    label: "System",
    items: [
      { label: "Rules & imports", icon: BookOpen, disabled: true },
      { label: "Settings", icon: Settings, disabled: true },
    ],
  },
];

export function SideNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();

  return (
    <nav className="flex h-full flex-col gap-6 overflow-y-auto px-4 py-6">
      <div className="flex items-center gap-2 px-2">
        <div
          aria-hidden
          className="h-6 w-6 rounded-[6px] bg-[--color-accent]"
        />
        <span className="text-[15px] font-medium tracking-tight">Folio</span>
      </div>

      <div className="flex flex-col gap-6">
        {groups.map((group) => (
          <div key={group.label} className="flex flex-col gap-1">
            <div className="px-2 text-[11px] font-medium tracking-[0.07em] text-[--color-fg-faint] uppercase">
              {group.label}
            </div>
            <ul className="flex flex-col gap-0.5">
              {group.items.map((item) => {
                const active =
                  item.href && pathname === item.href
                    ? true
                    : item.href && item.href !== "/"
                      ? pathname.startsWith(item.href)
                      : false;
                if (item.disabled || !item.href) {
                  return (
                    <li key={item.label}>
                      <span className="flex items-center gap-2 rounded-[6px] px-2 py-1.5 text-[13px] text-[--color-fg-faint]">
                        <item.icon className="h-4 w-4" />
                        <span>{item.label}</span>
                        <span className="ml-auto text-[10px] tracking-wider text-[--color-fg-faint] uppercase">
                          Planned
                        </span>
                      </span>
                    </li>
                  );
                }
                return (
                  <li key={item.label}>
                    <Link
                      href={item.href as never}
                      onClick={onNavigate}
                      className={cn(
                        "flex items-center gap-2 rounded-[6px] px-2 py-1.5 text-[13px] transition-colors duration-150",
                        active
                          ? "border-l-2 border-[--color-accent] bg-[--color-surface-subtle] pl-[7px] font-medium text-[--color-fg]"
                          : "text-[--color-fg-muted] hover:bg-[--color-surface-subtle] hover:text-[--color-fg]"
                      )}
                    >
                      <item.icon className="h-4 w-4" />
                      <span>{item.label}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </div>
    </nav>
  );
}

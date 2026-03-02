"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { Building2 } from "lucide-react";

interface Account {
  id: string;
  name: string;
  broker: string;
}

export function AccountFilter({
  value,
  onChange,
}: {
  value: string | undefined;
  onChange: (accountId: string | undefined) => void;
}) {
  const [accounts, setAccounts] = useState<Account[]>([]);

  useEffect(() => {
    fetch("/api/accounts")
      .then((r) => r.json())
      .then(setAccounts)
      .catch(() => {});
  }, []);

  if (accounts.length <= 1) return null;

  return (
    <div className="flex items-center gap-1.5">
      <Building2 size={14} className="text-text-tertiary shrink-0" />
      <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
        <button
          onClick={() => onChange(undefined)}
          className={cn(
            "px-2.5 py-1 text-[10px] font-mono font-medium rounded-md transition-all whitespace-nowrap",
            !value
              ? "bg-accent text-bg-primary"
              : "text-text-tertiary hover:text-text-secondary"
          )}
        >
          All
        </button>
        {accounts.map((a) => (
          <button
            key={a.id}
            onClick={() => onChange(a.id)}
            className={cn(
              "px-2.5 py-1 text-[10px] font-mono font-medium rounded-md transition-all whitespace-nowrap",
              value === a.id
                ? "bg-accent text-bg-primary"
                : "text-text-tertiary hover:text-text-secondary"
            )}
          >
            {a.name}
          </button>
        ))}
      </div>
    </div>
  );
}

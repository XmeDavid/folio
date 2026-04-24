import type { AccountKind } from "@/lib/api/client";

// Shared labels for AccountKind so we don't re-spell them per screen.
export const ACCOUNT_KINDS: { value: AccountKind; label: string }[] = [
  { value: "checking", label: "Checking" },
  { value: "savings", label: "Savings" },
  { value: "cash", label: "Cash" },
  { value: "credit_card", label: "Credit card" },
  { value: "brokerage", label: "Brokerage" },
  { value: "crypto_wallet", label: "Crypto wallet" },
  { value: "loan", label: "Loan" },
  { value: "mortgage", label: "Mortgage" },
  { value: "asset", label: "Asset" },
  { value: "pillar_2", label: "Pillar 2" },
  { value: "pillar_3a", label: "Pillar 3a" },
  { value: "other", label: "Other" },
];

const LABEL_BY_VALUE: Record<string, string> = Object.fromEntries(
  ACCOUNT_KINDS.map((k) => [k.value, k.label])
);

export function accountKindLabel(value: AccountKind): string {
  return LABEL_BY_VALUE[value] ?? value;
}

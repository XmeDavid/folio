import type { Account, AccountGroup, AccountKind } from "@/lib/api/client";
import { addDecimalStrings } from "@/lib/decimal";
import { convertAmount, type FxRate } from "@/lib/fx";

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

export type BalanceUnit = {
  id: string;
  type: "account" | "group";
  label: string;
  amount: string;
  currency: string;
  accountCount: number;
  convertedToBase: boolean;
  missingCurrencies: string[];
};

export function buildBalanceUnits({
  accounts,
  groups,
  baseCurrency,
  rates,
}: {
  accounts: Account[];
  groups: AccountGroup[];
  baseCurrency: string;
  rates: Record<string, FxRate>;
}): BalanceUnit[] {
  const groupById = new Map(groups.map((group) => [group.id, group]));
  const accountsByGroup = new Map<string, Account[]>();
  const units: BalanceUnit[] = [];

  for (const account of accounts) {
    const groupId = account.accountGroupId;
    const group = groupId ? groupById.get(groupId) : undefined;
    if (group?.aggregateBalances) {
      const grouped = accountsByGroup.get(group.id) ?? [];
      grouped.push(account);
      accountsByGroup.set(group.id, grouped);
      continue;
    }
    units.push(accountBalanceUnit(account));
  }

  for (const group of groups) {
    if (!group.aggregateBalances) continue;
    const grouped = accountsByGroup.get(group.id) ?? [];
    if (grouped.length === 0) continue;
    units.push(groupBalanceUnit(group, grouped, baseCurrency, rates));
  }

  return units.sort((a, b) => a.label.localeCompare(b.label));
}

export function netWorthFromBalanceUnits(
  units: BalanceUnit[],
  baseCurrency: string,
  rates: Record<string, FxRate>
): { total: string; missingCurrencies: string[] } {
  let total = "0";
  const missing = new Set<string>();
  for (const unit of units) {
    const converted = convertAmount(
      unit.amount,
      unit.currency,
      baseCurrency,
      rates
    );
    if (converted == null) {
      missing.add(unit.currency.toUpperCase());
      continue;
    }
    total = addDecimalStrings(total, converted);
    for (const currency of unit.missingCurrencies) missing.add(currency);
  }
  return { total, missingCurrencies: [...missing].sort() };
}

function accountBalanceUnit(account: Account): BalanceUnit {
  return {
    id: account.id,
    type: "account",
    label: account.name,
    amount: account.balance,
    currency: account.currency.toUpperCase(),
    accountCount: 1,
    convertedToBase: false,
    missingCurrencies: [],
  };
}

function groupBalanceUnit(
  group: AccountGroup,
  accounts: Account[],
  baseCurrency: string,
  rates: Record<string, FxRate>
): BalanceUnit {
  const currencies = [
    ...new Set(accounts.map((account) => account.currency.toUpperCase())),
  ];
  if (currencies.length === 1) {
    return {
      id: group.id,
      type: "group",
      label: group.name,
      amount: sumAccounts(accounts),
      currency: currencies[0] ?? baseCurrency.toUpperCase(),
      accountCount: accounts.length,
      convertedToBase: false,
      missingCurrencies: [],
    };
  }

  let amount = "0";
  const missing = new Set<string>();
  for (const account of accounts) {
    const converted = convertAmount(
      account.balance,
      account.currency,
      baseCurrency,
      rates
    );
    if (converted == null) {
      missing.add(account.currency.toUpperCase());
      continue;
    }
    amount = addDecimalStrings(amount, converted);
  }

  return {
    id: group.id,
    type: "group",
    label: group.name,
    amount,
    currency: baseCurrency.toUpperCase(),
    accountCount: accounts.length,
    convertedToBase: true,
    missingCurrencies: [...missing].sort(),
  };
}

function sumAccounts(accounts: Account[]): string {
  return accounts.reduce(
    (total, account) => addDecimalStrings(total, account.balance),
    "0"
  );
}

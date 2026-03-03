import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { bankingTransactions, accounts } from "@/db/schema";
import { eq, and, gte, lte, desc } from "drizzle-orm";

interface FxConversionPair {
  id: string;
  date: string;
  accountId: string;
  accountName: string;
  fromCurrency: string;
  fromAmount: string;
  toCurrency: string;
  toAmount: string;
  description: string;
}

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const accountId = url.searchParams.get("accountId");
  const from = url.searchParams.get("from");
  const to = url.searchParams.get("to");
  const limit = parseInt(url.searchParams.get("limit") || "50");
  const offset = parseInt(url.searchParams.get("offset") || "0");

  const conditions = [eq(bankingTransactions.transferType, "fx")];
  if (accountId) conditions.push(eq(bankingTransactions.accountId, accountId));
  if (from) conditions.push(gte(bankingTransactions.date, new Date(from)));
  if (to) conditions.push(lte(bankingTransactions.date, new Date(to)));

  const rows = await db
    .select({
      id: bankingTransactions.id,
      accountId: bankingTransactions.accountId,
      accountName: accounts.name,
      date: bankingTransactions.date,
      amount: bankingTransactions.amount,
      currency: bankingTransactions.currency,
      description: bankingTransactions.description,
    })
    .from(bankingTransactions)
    .leftJoin(accounts, eq(bankingTransactions.accountId, accounts.id))
    .where(and(...conditions))
    .orderBy(desc(bankingTransactions.date));

  // Pair FX conversions: match outgoing (negative) with incoming (positive)
  // by same account, different currency, within 60s window
  const paired = new Set<string>();
  const pairs: FxConversionPair[] = [];
  const unpaired: FxConversionPair[] = [];

  const outgoing = rows.filter((r) => parseFloat(r.amount) < 0);
  const incoming = rows.filter((r) => parseFloat(r.amount) >= 0);

  for (const out of outgoing) {
    if (paired.has(out.id)) continue;

    const outTime = out.date.getTime();

    const match = incoming.find((inc) => {
      if (paired.has(inc.id)) return false;
      if (inc.accountId !== out.accountId) return false; // same account
      if (inc.currency === out.currency) return false; // different currency
      if (Math.abs(inc.date.getTime() - outTime) > 60000) return false; // within 60s
      return true;
    });

    if (match) {
      paired.add(out.id);
      paired.add(match.id);
      pairs.push({
        id: out.id,
        date: out.date.toISOString(),
        accountId: out.accountId,
        accountName: out.accountName || "Unknown",
        fromCurrency: out.currency,
        fromAmount: Math.abs(parseFloat(out.amount)).toFixed(2),
        toCurrency: match.currency,
        toAmount: parseFloat(match.amount).toFixed(2),
        description: out.description,
      });
    } else {
      // Unpaired FX transaction
      const amt = parseFloat(out.amount);
      unpaired.push({
        id: out.id,
        date: out.date.toISOString(),
        accountId: out.accountId,
        accountName: out.accountName || "Unknown",
        fromCurrency: amt < 0 ? out.currency : "",
        fromAmount: Math.abs(amt).toFixed(2),
        toCurrency: amt >= 0 ? out.currency : "",
        toAmount: amt >= 0 ? amt.toFixed(2) : "",
        description: out.description,
      });
    }
  }

  // Any incoming FX that weren't paired
  for (const inc of incoming) {
    if (paired.has(inc.id)) continue;
    const amt = parseFloat(inc.amount);
    unpaired.push({
      id: inc.id,
      date: inc.date.toISOString(),
      accountId: inc.accountId,
      accountName: inc.accountName || "Unknown",
      fromCurrency: "",
      fromAmount: "",
      toCurrency: inc.currency,
      toAmount: amt.toFixed(2),
      description: inc.description,
    });
  }

  const all = [...pairs, ...unpaired].sort(
    (a, b) => new Date(b.date).getTime() - new Date(a.date).getTime()
  );

  const total = all.length;
  const page = all.slice(offset, offset + limit);

  return NextResponse.json({ data: page, total });
}

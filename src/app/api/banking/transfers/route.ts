import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { bankingTransactions, accounts } from "@/db/schema";
import { eq, and, gte, lte, desc, sql, or } from "drizzle-orm";

interface TransferPair {
  id: string;
  date: string;
  fromAccountId: string;
  fromAccountName: string;
  toAccountId: string;
  toAccountName: string;
  amount: string;
  currency: string;
  description: string;
}

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const accountId = url.searchParams.get("accountId");
  const from = url.searchParams.get("from");
  const to = url.searchParams.get("to");
  const limit = parseInt(url.searchParams.get("limit") || "50");
  const offset = parseInt(url.searchParams.get("offset") || "0");

  // Fetch all internal transfers
  const conditions = [
    eq(bankingTransactions.transferType, "internal"),
  ];
  if (accountId) {
    // Show transfers involving this account (either side)
    conditions.push(eq(bankingTransactions.accountId, accountId));
  }
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

  // Pair transfers: match outgoing (negative) with incoming (positive)
  // by same calendar date, same absolute amount, same currency, different account
  const paired = new Set<string>();
  const pairs: TransferPair[] = [];
  const unpaired: TransferPair[] = [];

  const outgoing = rows.filter((r) => parseFloat(r.amount) < 0);
  const incoming = rows.filter((r) => parseFloat(r.amount) >= 0);

  for (const out of outgoing) {
    if (paired.has(out.id)) continue;

    const outDate = out.date.toISOString().slice(0, 10);
    const outAbs = Math.abs(parseFloat(out.amount));

    // Find matching incoming transaction
    const match = incoming.find((inc) => {
      if (paired.has(inc.id)) return false;
      if (inc.accountId === out.accountId) return false;
      if (inc.currency !== out.currency) return false;
      const incDate = inc.date.toISOString().slice(0, 10);
      if (incDate !== outDate) return false;
      const incAbs = Math.abs(parseFloat(inc.amount));
      // Allow small rounding tolerance (0.01)
      return Math.abs(outAbs - incAbs) < 0.02;
    });

    if (match) {
      paired.add(out.id);
      paired.add(match.id);
      pairs.push({
        id: out.id,
        date: out.date.toISOString(),
        fromAccountId: out.accountId,
        fromAccountName: out.accountName || "Unknown",
        toAccountId: match.accountId,
        toAccountName: match.accountName || "Unknown",
        amount: outAbs.toFixed(2),
        currency: out.currency,
        description: out.description,
      });
    } else {
      // Unmatched outgoing — show as one-sided transfer
      unpaired.push({
        id: out.id,
        date: out.date.toISOString(),
        fromAccountId: out.accountId,
        fromAccountName: out.accountName || "Unknown",
        toAccountId: "",
        toAccountName: out.description,
        amount: outAbs.toFixed(2),
        currency: out.currency,
        description: out.description,
      });
    }
  }

  // Any incoming transfers that weren't paired
  for (const inc of incoming) {
    if (paired.has(inc.id)) continue;
    unpaired.push({
      id: inc.id,
      date: inc.date.toISOString(),
      fromAccountId: "",
      fromAccountName: inc.description,
      toAccountId: inc.accountId,
      toAccountName: inc.accountName || "Unknown",
      amount: parseFloat(inc.amount).toFixed(2),
      currency: inc.currency,
      description: inc.description,
    });
  }

  // Combine and sort by date descending
  const all = [...pairs, ...unpaired].sort(
    (a, b) => new Date(b.date).getTime() - new Date(a.date).getTime()
  );

  const total = all.length;
  const page = all.slice(offset, offset + limit);

  return NextResponse.json({ data: page, total });
}

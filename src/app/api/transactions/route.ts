import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { transactions, accounts } from "@/db/schema";
import { eq, desc, asc, and, gte, lte, sql, inArray } from "drizzle-orm";
import { z } from "zod/v4";
import { getRawTickersForCanonical, normalizeTicker } from "@/lib/portfolio/ticker-aliases";

const createTransactionSchema = z.object({
  accountId: z.string().uuid(),
  date: z.string(),
  ticker: z.string().nullable().optional(),
  type: z.string(),
  quantity: z.string().nullable().optional(),
  unitPrice: z.string().nullable().optional(),
  totalAmount: z.string(),
  currency: z.string().default("USD"),
  commission: z.string().default("0"),
  fxRateOriginal: z.string().nullable().optional(),
});

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const accountId = url.searchParams.get("accountId");
  const ticker = url.searchParams.get("ticker");
  const type = url.searchParams.get("type");
  const from = url.searchParams.get("from");
  const to = url.searchParams.get("to");
  const limit = parseInt(url.searchParams.get("limit") || "500");
  const offset = parseInt(url.searchParams.get("offset") || "0");
  const sortBy = url.searchParams.get("sortBy") || "date";
  const sortDir = (url.searchParams.get("sortDir") || "desc").toLowerCase() === "asc" ? "asc" : "desc";

  const conditions = [];
  if (accountId) conditions.push(eq(transactions.accountId, accountId));
  if (ticker) {
    const canonical = normalizeTicker(ticker);
    const rawTickers = getRawTickersForCanonical(canonical);
    if (rawTickers.length === 1) {
      conditions.push(eq(transactions.ticker, rawTickers[0]));
    } else {
      conditions.push(inArray(transactions.ticker, rawTickers));
    }
  }
  if (type) conditions.push(eq(transactions.type, type));
  if (from) conditions.push(gte(transactions.date, new Date(from)));
  if (to) conditions.push(lte(transactions.date, new Date(to)));

  const where = conditions.length > 0 ? and(...conditions) : undefined;

  const orderBy = (() => {
    switch (sortBy) {
      case "ticker":
        return sortDir === "asc" ? asc(transactions.ticker) : desc(transactions.ticker);
      case "type":
        return sortDir === "asc" ? asc(transactions.type) : desc(transactions.type);
      case "quantity":
        return sortDir === "asc" ? asc(transactions.quantity) : desc(transactions.quantity);
      case "unitPrice":
        return sortDir === "asc" ? asc(transactions.unitPrice) : desc(transactions.unitPrice);
      case "totalAmount":
        return sortDir === "asc" ? asc(transactions.totalAmount) : desc(transactions.totalAmount);
      case "fxRateOriginal":
        return sortDir === "asc" ? asc(transactions.fxRateOriginal) : desc(transactions.fxRateOriginal);
      case "broker":
        return sortDir === "asc" ? asc(accounts.broker) : desc(accounts.broker);
      case "date":
      default:
        return sortDir === "asc" ? asc(transactions.date) : desc(transactions.date);
    }
  })();

  const [result, countResult] = await Promise.all([
    db
      .select({
        transaction: transactions,
        accountName: accounts.name,
        broker: accounts.broker,
      })
      .from(transactions)
      .leftJoin(accounts, eq(transactions.accountId, accounts.id))
      .where(where)
      .orderBy(orderBy)
      .limit(limit)
      .offset(offset),
    db
      .select({ count: sql<number>`count(*)` })
      .from(transactions)
      .where(where),
  ]);

  return NextResponse.json({
    data: result,
    total: Number(countResult[0].count),
    limit,
    offset,
  });
}

export async function POST(req: NextRequest) {
  const body = await req.json();
  const parsed = createTransactionSchema.parse(body);

  const [created] = await db
    .insert(transactions)
    .values({
      ...parsed,
      date: new Date(parsed.date),
    })
    .returning();

  return NextResponse.json(created, { status: 201 });
}

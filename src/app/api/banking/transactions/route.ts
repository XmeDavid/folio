import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { bankingTransactions, accounts } from "@/db/schema";
import { eq, desc, asc, and, gte, lte, sql, ilike, or } from "drizzle-orm";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const accountId = url.searchParams.get("accountId");
  const category = url.searchParams.get("category");
  const merchant = url.searchParams.get("merchant");
  const from = url.searchParams.get("from");
  const to = url.searchParams.get("to");
  const status = url.searchParams.get("status");
  const transferType = url.searchParams.get("transferType");
  const excludeTransfers = url.searchParams.get("excludeTransfers") !== "false"; // default true
  const excludeFx = url.searchParams.get("excludeFx") !== "false"; // default true
  const tag = url.searchParams.get("tag");
  const search = url.searchParams.get("search");
  const limit = parseInt(url.searchParams.get("limit") || "50");
  const offset = parseInt(url.searchParams.get("offset") || "0");
  const sortBy = url.searchParams.get("sortBy") || "date";
  const sortDir = (url.searchParams.get("sortDir") || "desc").toLowerCase() === "asc" ? "asc" : "desc";

  const conditions = [];
  if (accountId) conditions.push(eq(bankingTransactions.accountId, accountId));
  if (category) conditions.push(eq(bankingTransactions.category, category));
  if (merchant) conditions.push(ilike(bankingTransactions.merchant, `%${merchant}%`));
  if (from) conditions.push(gte(bankingTransactions.date, new Date(from)));
  if (to) conditions.push(lte(bankingTransactions.date, new Date(to)));
  if (status) conditions.push(eq(bankingTransactions.status, status));
  if (transferType) {
    conditions.push(eq(bankingTransactions.transferType, transferType));
  } else {
    const excludeTypes: string[] = [];
    if (excludeTransfers) excludeTypes.push("internal");
    if (excludeFx) excludeTypes.push("fx");
    if (excludeTypes.length > 0) {
      const list = excludeTypes.map((t) => `'${t}'`).join(", ");
      conditions.push(
        sql`(${bankingTransactions.transferType} IS NULL OR ${bankingTransactions.transferType} NOT IN (${sql.raw(list)}))`
      );
    }
  }
  if (tag) {
    conditions.push(sql`${bankingTransactions.tags} @> ARRAY[${tag}]::text[]`);
  }
  if (search) {
    conditions.push(
      or(
        ilike(bankingTransactions.description, `%${search}%`),
        ilike(bankingTransactions.merchant, `%${search}%`)
      )
    );
  }

  const where = conditions.length > 0 ? and(...conditions) : undefined;

  const orderBy = (() => {
    const dir = sortDir === "asc" ? asc : desc;
    switch (sortBy) {
      case "amount":
        return dir(bankingTransactions.amount);
      case "merchant":
        return dir(bankingTransactions.merchant);
      case "category":
        return dir(bankingTransactions.category);
      case "description":
        return dir(bankingTransactions.description);
      case "date":
      default:
        return dir(bankingTransactions.date);
    }
  })();

  const [result, countResult] = await Promise.all([
    db
      .select({
        transaction: bankingTransactions,
        accountName: accounts.name,
        accountType: accounts.type,
      })
      .from(bankingTransactions)
      .leftJoin(accounts, eq(bankingTransactions.accountId, accounts.id))
      .where(where)
      .orderBy(orderBy)
      .limit(limit)
      .offset(offset),
    db
      .select({ count: sql<number>`count(*)` })
      .from(bankingTransactions)
      .where(where),
  ]);

  return NextResponse.json({
    data: result,
    total: Number(countResult[0].count),
    limit,
    offset,
  });
}

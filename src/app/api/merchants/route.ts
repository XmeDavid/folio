import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { bankingTransactions, merchantOverrides } from "@/db/schema";
import { sql } from "drizzle-orm";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const search = url.searchParams.get("search") || "";
  const sortBy = url.searchParams.get("sortBy") || "count";
  const sortDir = (url.searchParams.get("sortDir") || "desc").toLowerCase() === "asc" ? "asc" : "desc";
  const limit = parseInt(url.searchParams.get("limit") || "100");
  const offset = parseInt(url.searchParams.get("offset") || "0");

  const searchFilter = search
    ? sql`AND bt.merchant ILIKE ${"%" + search + "%"}`
    : sql``;

  const orderColumn = (() => {
    switch (sortBy) {
      case "name": return sql`merchant`;
      case "total": return sql`total_spent`;
      case "count":
      default: return sql`tx_count`;
    }
  })();

  const orderDir = sortDir === "asc" ? sql`ASC` : sql`DESC`;

  const result = await db.execute(sql`
    SELECT
      bt.merchant,
      COUNT(*)::int AS tx_count,
      SUM(CASE WHEN bt.amount::numeric < 0 THEN ABS(bt.amount::numeric) * COALESCE(
        (SELECT fr.rate::numeric FROM fx_rates fr
         WHERE fr.base = bt.currency AND fr.target = 'CHF'
           AND fr.date <= bt.date::date
         ORDER BY fr.date DESC LIMIT 1),
        CASE WHEN bt.currency = 'CHF' THEN 1 ELSE NULL END
      ) ELSE 0 END) AS total_spent,
      mo.category AS override_category
    FROM banking_transactions bt
    LEFT JOIN merchant_overrides mo ON mo.merchant_name = bt.merchant
    WHERE bt.merchant IS NOT NULL
      ${searchFilter}
    GROUP BY bt.merchant, mo.category
    ORDER BY ${orderColumn} ${orderDir}
    LIMIT ${limit} OFFSET ${offset}
  `);

  const countResult = await db.execute(sql`
    SELECT COUNT(DISTINCT merchant)::int AS total
    FROM banking_transactions
    WHERE merchant IS NOT NULL
      ${searchFilter}
  `);

  return NextResponse.json({
    data: (result as any).rows ?? result,
    total: ((countResult as any).rows ?? countResult)[0]?.total ?? 0,
  });
}

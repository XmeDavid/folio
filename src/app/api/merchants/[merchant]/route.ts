import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { merchantOverrides, bankingTransactions } from "@/db/schema";
import { eq, and, sql } from "drizzle-orm";

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ merchant: string }> }
) {
  const { merchant } = await params;
  const merchantName = decodeURIComponent(merchant);
  const body = await req.json();
  const { category } = body;

  if (category === undefined) {
    return NextResponse.json({ error: "category is required" }, { status: 400 });
  }

  // Upsert merchant override
  if (category === null) {
    // Remove override
    await db
      .delete(merchantOverrides)
      .where(eq(merchantOverrides.merchantName, merchantName));
  } else {
    await db
      .insert(merchantOverrides)
      .values({ merchantName, category })
      .onConflictDoUpdate({
        target: merchantOverrides.merchantName,
        set: { category },
      });
  }

  // Bulk update non-manual transactions for this merchant
  const updateResult = await db.execute(sql`
    UPDATE banking_transactions
    SET category = ${category}
    WHERE merchant = ${merchantName}
      AND category_manual = false
  `);

  return NextResponse.json({
    merchantName,
    category,
    transactionsUpdated: (updateResult as any).rowCount ?? 0,
  });
}

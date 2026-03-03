import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { accounts, transactions, bankingTransactions } from "@/db/schema";
import { eq, sql } from "drizzle-orm";
import { z } from "zod";

const updateSchema = z.object({
  name: z.string().min(1).optional(),
  type: z.string().optional(),
  baseCurrency: z.string().optional(),
});

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> }
) {
  try {
    const { id } = await params;
    const body = await req.json();
    const data = updateSchema.parse(body);

    const updates = Object.fromEntries(
      Object.entries(data).filter(([, v]) => v !== undefined)
    );

    if (Object.keys(updates).length === 0) {
      return NextResponse.json({ error: "No fields to update" }, { status: 400 });
    }

    const [updated] = await db
      .update(accounts)
      .set(updates)
      .where(eq(accounts.id, id))
      .returning();

    if (!updated) {
      return NextResponse.json({ error: "Account not found" }, { status: 404 });
    }

    return NextResponse.json(updated);
  } catch (err) {
    if (err instanceof z.ZodError) {
      return NextResponse.json({ error: err.issues }, { status: 400 });
    }
    return NextResponse.json({ error: "Failed to update account" }, { status: 500 });
  }
}

export async function DELETE(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;

  // Check transaction counts
  const [investmentResult] = await db
    .select({ count: sql<number>`count(*)` })
    .from(transactions)
    .where(eq(transactions.accountId, id));

  const [bankingResult] = await db
    .select({ count: sql<number>`count(*)` })
    .from(bankingTransactions)
    .where(eq(bankingTransactions.accountId, id));

  const totalCount = Number(investmentResult.count) + Number(bankingResult.count);

  if (totalCount > 0) {
    return NextResponse.json(
      {
        error: `Account has ${totalCount} transactions. Delete them first.`,
        transactionCount: totalCount,
      },
      { status: 409 }
    );
  }

  const [deleted] = await db
    .delete(accounts)
    .where(eq(accounts.id, id))
    .returning();

  if (!deleted) {
    return NextResponse.json({ error: "Account not found" }, { status: 404 });
  }

  return NextResponse.json({ ok: true });
}

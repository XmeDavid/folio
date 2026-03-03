import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { bankingTransactions } from "@/db/schema";
import { eq } from "drizzle-orm";

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> }
) {
  const { id } = await params;
  const body = await req.json();
  const { category, tags } = body;

  const updates: Record<string, unknown> = {};

  if (category !== undefined) {
    updates.category = category;
    updates.categoryManual = true;
  }

  if (tags !== undefined) {
    if (!Array.isArray(tags) || !tags.every((t: unknown) => typeof t === "string")) {
      return NextResponse.json({ error: "tags must be an array of strings" }, { status: 400 });
    }
    updates.tags = tags;
  }

  if (Object.keys(updates).length === 0) {
    return NextResponse.json({ error: "No fields to update" }, { status: 400 });
  }

  const [updated] = await db
    .update(bankingTransactions)
    .set(updates)
    .where(eq(bankingTransactions.id, id))
    .returning();

  if (!updated) {
    return NextResponse.json({ error: "Transaction not found" }, { status: 404 });
  }

  return NextResponse.json({ data: updated });
}

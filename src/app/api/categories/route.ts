import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { categories, bankingTransactions } from "@/db/schema";
import { asc, sql } from "drizzle-orm";

export async function GET() {
  // Get categories from the table
  const tableCategories = await db
    .select()
    .from(categories)
    .orderBy(asc(categories.name));

  // Also get unique categories in use from transactions (parser-assigned)
  const usedResult = await db.execute(sql`
    SELECT DISTINCT category FROM banking_transactions
    WHERE category IS NOT NULL
    ORDER BY category
  `);
  const usedCategories: string[] = ((usedResult as any).rows ?? usedResult).map(
    (r: any) => r.category
  );

  // Merge: table categories + any in-use categories not in the table
  const tableNames = new Set(tableCategories.map((c) => c.name));
  const allNames = [...tableNames];
  for (const name of usedCategories) {
    if (!tableNames.has(name)) allNames.push(name);
  }
  allNames.sort();

  return NextResponse.json({
    data: allNames,
  });
}

export async function POST(req: NextRequest) {
  const body = await req.json();
  const { name } = body;

  if (!name || typeof name !== "string" || !name.trim()) {
    return NextResponse.json({ error: "name is required" }, { status: 400 });
  }

  const trimmed = name.trim();

  // Upsert — don't error if it already exists
  await db
    .insert(categories)
    .values({ name: trimmed })
    .onConflictDoNothing();

  return NextResponse.json({ name: trimmed });
}

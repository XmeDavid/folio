import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { accounts, transactions, bankingTransactions } from "@/db/schema";
import { sql, eq } from "drizzle-orm";
import { z } from "zod";

export async function GET() {
  const result = await db
    .select({
      id: accounts.id,
      name: accounts.name,
      broker: accounts.broker,
      type: accounts.type,
      baseCurrency: accounts.baseCurrency,
      createdAt: accounts.createdAt,
      investmentCount: sql<number>`(select count(*) from transactions where account_id = ${accounts.id})`.as("investment_count"),
      bankingCount: sql<number>`(select count(*) from banking_transactions where account_id = ${accounts.id})`.as("banking_count"),
    })
    .from(accounts)
    .orderBy(accounts.name);

  return NextResponse.json(result);
}

const createSchema = z.object({
  name: z.string().min(1),
  broker: z.string().min(1),
  type: z.string().default("investment"),
  baseCurrency: z.string().default("USD"),
});

export async function POST(req: NextRequest) {
  try {
    const body = await req.json();
    const data = createSchema.parse(body);

    const [created] = await db.insert(accounts).values(data).returning();
    return NextResponse.json(created, { status: 201 });
  } catch (err) {
    if (err instanceof z.ZodError) {
      return NextResponse.json({ error: err.issues }, { status: 400 });
    }
    return NextResponse.json({ error: "Failed to create account" }, { status: 500 });
  }
}

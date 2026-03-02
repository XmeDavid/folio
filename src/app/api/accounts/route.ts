import { NextResponse } from "next/server";
import { db } from "@/db";
import { accounts } from "@/db/schema";

export async function GET() {
  const result = await db.select().from(accounts).orderBy(accounts.name);
  return NextResponse.json(result);
}

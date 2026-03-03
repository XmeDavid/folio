import { NextResponse } from "next/server";
import { db } from "@/db";
import { sql } from "drizzle-orm";

export async function GET() {
  // Get all unique tags across transactions
  const result = await db.execute(sql`
    SELECT DISTINCT unnest(tags) AS tag
    FROM banking_transactions
    WHERE array_length(tags, 1) > 0
    ORDER BY tag
  `);

  const tags: string[] = ((result as any).rows ?? result).map(
    (r: any) => r.tag
  );

  return NextResponse.json({ data: tags });
}

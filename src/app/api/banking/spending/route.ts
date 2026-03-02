import { NextRequest, NextResponse } from "next/server";
import { getSpendingBreakdown } from "@/lib/banking/spending";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const from = url.searchParams.get("from") ?? undefined;
  const to = url.searchParams.get("to") ?? undefined;
  const accountId = url.searchParams.get("accountId") ?? undefined;
  const excludeTransfers = url.searchParams.get("excludeTransfers") !== "false";

  const result = await getSpendingBreakdown({
    from,
    to,
    accountId,
    excludeTransfers,
  });

  return NextResponse.json(result);
}

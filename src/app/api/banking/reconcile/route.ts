import { NextResponse } from "next/server";
import { reconcileTransfers } from "@/lib/banking/reconcile-transfers";

export async function POST() {
  const updated = await reconcileTransfers();
  return NextResponse.json({ updated });
}

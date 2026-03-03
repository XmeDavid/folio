import { NextResponse } from "next/server";
import { reconcileTransfers } from "@/lib/banking/reconcile-transfers";
import { reconcileFxConversions } from "@/lib/banking/reconcile-fx";

export async function POST() {
  const transfers = await reconcileTransfers();
  const fx = await reconcileFxConversions();
  return NextResponse.json({ transfers, fx });
}

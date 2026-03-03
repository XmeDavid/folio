import { db } from "@/db";
import { sql } from "drizzle-orm";

/**
 * Reconcile transfer types across all banking transactions.
 *
 * A transaction should only be "internal" if both sides of the transfer
 * exist in tracked accounts. This function does two things:
 *
 * 1. MARK as internal: if a transaction has a matching counterpart in
 *    another tracked account (same day, same abs amount, same currency,
 *    opposite sign), mark both sides as internal. This uses the amount
 *    heuristic directly — no need for the counterpart to already be
 *    tagged, which avoids import-order issues.
 *
 * 2. UN-MARK false positives: if a transaction is marked as "internal"
 *    but no matching counterpart exists in any tracked account, clear it
 *    (the money left our tracked accounts — not an internal transfer).
 *
 * Returns { marked, unmarked } counts.
 */
export async function reconcileTransfers(): Promise<{ marked: number; unmarked: number }> {
  // Step 1: Mark both sides of matching cross-account pairs as internal.
  // A pair matches when: different account, same currency, same day,
  // opposite signs, and amounts cancel out (within tolerance).
  const markResult = await db.execute(sql`
    UPDATE banking_transactions bt
    SET transfer_type = 'internal'
    WHERE bt.transfer_type IS DISTINCT FROM 'internal'
      AND EXISTS (
        SELECT 1 FROM banking_transactions other
        WHERE other.account_id != bt.account_id
          AND other.currency = bt.currency
          AND DATE(other.date) = DATE(bt.date)
          AND ABS(other.amount::numeric + bt.amount::numeric) < 0.02
          AND SIGN(other.amount::numeric) != SIGN(bt.amount::numeric)
      )
  `);

  // Step 2: Un-mark transactions flagged as internal that have no
  // matching counterpart in any other tracked account
  const unmarkResult = await db.execute(sql`
    UPDATE banking_transactions bt
    SET transfer_type = NULL
    WHERE bt.transfer_type = 'internal'
      AND NOT EXISTS (
        SELECT 1 FROM banking_transactions other
        WHERE other.account_id != bt.account_id
          AND other.currency = bt.currency
          AND DATE(other.date) = DATE(bt.date)
          AND ABS(other.amount::numeric + bt.amount::numeric) < 0.02
          AND SIGN(other.amount::numeric) != SIGN(bt.amount::numeric)
      )
  `);

  return {
    marked: (markResult as any).rowCount ?? 0,
    unmarked: (unmarkResult as any).rowCount ?? 0,
  };
}

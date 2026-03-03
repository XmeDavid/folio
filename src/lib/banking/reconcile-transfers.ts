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
 *    opposite sign), and at least one side is already marked internal
 *    by a parser, mark the other side too.
 *
 * 2. UN-MARK false positives: if a transaction is marked as "internal"
 *    but no matching counterpart exists in any tracked account, clear it
 *    (the money left our tracked accounts — not an internal transfer).
 *
 * Returns { marked, unmarked } counts.
 */
export async function reconcileTransfers(): Promise<{ marked: number; unmarked: number }> {
  // Step 1: Mark counterparts of known internal transfers
  const markResult = await db.execute(sql`
    UPDATE banking_transactions bt
    SET transfer_type = 'internal'
    WHERE bt.transfer_type IS DISTINCT FROM 'internal'
      AND EXISTS (
        SELECT 1 FROM banking_transactions other
        WHERE other.transfer_type = 'internal'
          AND other.account_id != bt.account_id
          AND other.currency = bt.currency
          AND DATE(other.date) = DATE(bt.date)
          AND ABS(other.amount::numeric + bt.amount::numeric) < 0.02
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
      )
  `);

  return {
    marked: (markResult as any).rowCount ?? 0,
    unmarked: (unmarkResult as any).rowCount ?? 0,
  };
}

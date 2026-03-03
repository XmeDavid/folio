import { db } from "@/db";
import { sql } from "drizzle-orm";

/**
 * Reconcile FX conversion transactions.
 *
 * FX conversions in Revolut appear as pairs within the same account:
 * e.g. -15.90 EUR and +18.79 USD at the same timestamp. These are not
 * real spending/income — money just changed currency.
 *
 * 1. MARK as 'fx': transactions with known markers (original_type = 'Câmbio',
 *    category containing 'FX Conversion') or that have a matching counterpart
 *    in the same account with different currency, opposite sign, within 60s.
 *    Guard: don't overwrite 'internal' transfers.
 *
 * 2. UN-MARK false positives: only for heuristically-matched ones (keep
 *    known markers like Câmbio permanently marked).
 *
 * Returns { marked, unmarked } counts.
 */
export async function reconcileFxConversions(): Promise<{ marked: number; unmarked: number }> {
  // Step 1: Mark FX conversions
  const markResult = await db.execute(sql`
    UPDATE banking_transactions bt
    SET transfer_type = 'fx'
    WHERE bt.transfer_type IS DISTINCT FROM 'fx'
      AND bt.transfer_type IS DISTINCT FROM 'internal'
      AND (
        bt.original_type = 'Câmbio'
        OR bt.category LIKE '%FX Conversion%'
        OR EXISTS (
          SELECT 1 FROM banking_transactions other
          WHERE other.account_id = bt.account_id
            AND other.currency != bt.currency
            AND ABS(EXTRACT(EPOCH FROM other.date - bt.date)) < 60
            AND SIGN(other.amount::numeric) != SIGN(bt.amount::numeric)
            AND (other.original_type = 'Câmbio' OR other.category LIKE '%FX Conversion%')
        )
      )
  `);

  // Step 2: Un-mark false positives — only heuristically-matched ones.
  // Transactions with known markers (Câmbio / FX Conversion) stay marked.
  const unmarkResult = await db.execute(sql`
    UPDATE banking_transactions bt
    SET transfer_type = NULL
    WHERE bt.transfer_type = 'fx'
      AND bt.original_type IS DISTINCT FROM 'Câmbio'
      AND (bt.category IS NULL OR bt.category NOT LIKE '%FX Conversion%')
      AND NOT EXISTS (
        SELECT 1 FROM banking_transactions other
        WHERE other.account_id = bt.account_id
          AND other.currency != bt.currency
          AND ABS(EXTRACT(EPOCH FROM other.date - bt.date)) < 60
          AND SIGN(other.amount::numeric) != SIGN(bt.amount::numeric)
      )
  `);

  return {
    marked: (markResult as any).rowCount ?? 0,
    unmarked: (unmarkResult as any).rowCount ?? 0,
  };
}

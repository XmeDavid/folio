/**
 * Sanity-check script: compare Revolut CSV FX rates against frankfurter.app
 * historical API rates for a random sample of USD-denominated transactions.
 *
 * Usage: DATABASE_URL="postgres://folio:folio@localhost:5432/folio" npx tsx src/scripts/validate-revolut-fx.ts
 */

import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";
import { transactions, accounts } from "../db/schema";
import { eq, isNotNull, and } from "drizzle-orm";

const BASE_URL = "https://api.frankfurter.app";

async function fetchRate(date: string, from: string, to: string): Promise<number> {
  const url = `${BASE_URL}/${date}?from=${from}&to=${to}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`API ${res.status} for ${url}`);
  const data = await res.json();
  return data.rates[to];
}

async function main() {
  const client = postgres(process.env.DATABASE_URL!);
  const db = drizzle(client);

  const revolutAccts = await db
    .select({ id: accounts.id })
    .from(accounts)
    .where(eq(accounts.broker, "Revolut"));

  if (revolutAccts.length === 0) {
    console.log("No Revolut accounts found.");
    await client.end();
    return;
  }

  const acctId = revolutAccts[0].id;

  const usdTxns = await db
    .select()
    .from(transactions)
    .where(
      and(
        eq(transactions.accountId, acctId),
        eq(transactions.currency, "USD"),
        isNotNull(transactions.fxRateOriginal)
      )
    );

  // Random sample of 15
  const shuffled = usdTxns.sort(() => Math.random() - 0.5).slice(0, 15);

  console.log("Comparing Revolut CSV FX (EUR/USD) vs frankfurter.app historical rates\n");
  console.log(
    "Date".padEnd(12),
    "Ticker".padEnd(8),
    "CSV FX".padEnd(10),
    "API FX".padEnd(10),
    "Delta".padEnd(10),
    "Delta %"
  );
  console.log("-".repeat(70));

  const deltas: number[] = [];

  for (const tx of shuffled) {
    const date = tx.date.toISOString().split("T")[0];
    const csvRate = parseFloat(tx.fxRateOriginal!);

    try {
      const apiRate = await fetchRate(date, "EUR", "USD");
      const delta = csvRate - apiRate;
      const deltaPercent = (delta / apiRate) * 100;
      deltas.push(Math.abs(deltaPercent));

      console.log(
        date.padEnd(12),
        (tx.ticker || "--").padEnd(8),
        csvRate.toFixed(4).padEnd(10),
        apiRate.toFixed(4).padEnd(10),
        delta.toFixed(4).padEnd(10),
        `${deltaPercent.toFixed(3)}%`
      );
    } catch (err) {
      console.log(date.padEnd(12), (tx.ticker || "--").padEnd(8), csvRate.toFixed(4).padEnd(10), "FAIL");
    }

    await new Promise((r) => setTimeout(r, 200));
  }

  if (deltas.length > 0) {
    const avgDelta = deltas.reduce((a, b) => a + b, 0) / deltas.length;
    const maxDelta = Math.max(...deltas);
    console.log("\n--- Summary ---");
    console.log(`Samples:   ${deltas.length}`);
    console.log(`Avg |Δ|:   ${avgDelta.toFixed(4)}%`);
    console.log(`Max |Δ|:   ${maxDelta.toFixed(4)}%`);
    console.log(
      maxDelta < 1
        ? "PASS: All deltas < 1% -- CSV FX is EUR/USD and matches API within intraday variance."
        : "WARN: Some deltas > 1% -- investigate outliers."
    );
  }

  await client.end();
}

main().catch(console.error);

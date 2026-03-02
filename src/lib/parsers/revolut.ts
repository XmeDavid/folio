import { parse } from "csv-parse/sync";
import type { NewTransaction } from "@/db/schema";

interface RawRevolutRow {
  Date: string;
  Ticker: string;
  Type: string;
  Quantity: string;
  "Price per share": string;
  "Total Amount": string;
  Currency: string;
  "FX Rate": string;
}

function parseAmount(raw: string): number {
  if (!raw) return 0;
  const cleaned = raw.replace(/^[A-Z]{3}\s*/, "").trim();
  return parseFloat(cleaned) || 0;
}

const TRADE_TYPES = new Set([
  "BUY - MARKET",
  "BUY - LIMIT",
  "SELL - MARKET",
  "SELL - LIMIT",
]);

export function parseRevolutCSV(
  csvContent: string,
  accountId: string
): NewTransaction[] {
  const records: RawRevolutRow[] = parse(csvContent, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
  });

  return records.map((row) => {
    const totalAmount = parseAmount(row["Total Amount"]);
    const unitPrice = parseAmount(row["Price per share"]);
    const quantity = row.Quantity ? parseFloat(row.Quantity) : null;

    let commission = 0;
    if (TRADE_TYPES.has(row.Type) && quantity && unitPrice) {
      const expected = Math.round(quantity * unitPrice * 100) / 100;
      const diff = Math.round((totalAmount - expected) * 100) / 100;
      // Revolut always charges at least $0.01 (SEC/regulatory fee) even on
      // "free" trades, so any non-zero difference is a real fee.
      if (Math.abs(diff) >= 0.005) {
        commission = Math.abs(diff);
      }
    }

    return {
      accountId,
      date: new Date(row.Date),
      ticker: row.Ticker || null,
      type: row.Type,
      quantity: quantity?.toString() ?? null,
      unitPrice: unitPrice ? unitPrice.toString() : null,
      totalAmount: totalAmount.toString(),
      currency: row.Currency,
      commission: commission.toString(),
      fxRateOriginal: row["FX Rate"] || null,
    };
  });
}

import { parse as parseCsv } from "csv-parse/sync";
import type { NewTransaction } from "@/db/schema";

interface IBKRJsonTransaction {
  date: string;
  symbol: string;
  quantity: number;
  unit_price: number;
  trade_amount_debited: number;
  commission: number;
}

interface IBKRJsonFile {
  account: string;
  currency: string;
  transactions: IBKRJsonTransaction[];
}

export interface ParsedIbkrImport {
  baseCurrency: string;
  sourceType: "json" | "activity_csv";
  transactions: NewTransaction[];
}

function parseIbkrDateTime(raw: string): Date {
  // IBKR activity date-time format: "YYYY-MM-DD, HH:mm:ss"
  // Example: "2025-12-03, 10:53:11"
  const normalized = raw.replace(",", "").trim().replace(" ", "T");
  const d = new Date(`${normalized}Z`);
  if (!Number.isNaN(d.getTime())) return d;

  // Fallback: date only.
  const justDate = raw.split(",")[0]?.trim();
  return new Date(`${justDate}T00:00:00Z`);
}

function parseIbkrFromJson(content: string, accountId: string): ParsedIbkrImport {
  const data: IBKRJsonFile = JSON.parse(content);

  const transactions: NewTransaction[] = data.transactions.map((tx) => ({
    accountId,
    date: new Date(`${tx.date}T00:00:00Z`),
    ticker: tx.symbol,
    type: "BUY",
    quantity: tx.quantity.toString(),
    unitPrice: tx.unit_price.toString(),
    totalAmount: tx.trade_amount_debited.toString(),
    currency: data.currency,
    commission: tx.commission.toString(),
    fxRateOriginal: null,
  }));

  return {
    baseCurrency: data.currency || "USD",
    sourceType: "json",
    transactions,
  };
}

function parseIbkrFromActivityCsv(
  content: string,
  accountId: string
): ParsedIbkrImport {
  const rows: string[][] = parseCsv(content, {
    skip_empty_lines: true,
    relax_column_count: true,
    trim: true,
  });

  let baseCurrency = "USD";
  const transactions: NewTransaction[] = [];

  for (const row of rows) {
    const section = row[0];
    const rowType = row[1];

    // Example:
    // Account Information,Data,Base Currency,CHF
    if (
      section === "Account Information" &&
      rowType === "Data" &&
      row[2] === "Base Currency" &&
      row[3]
    ) {
      baseCurrency = row[3].trim();
      continue;
    }

    // Parse actual order executions:
    // Stocks: Trades,Data,Order,Stocks,USD,GOOGL,"2025-12-03, 10:53:11",1,319.29,...
    // Forex : Trades,Data,Order,Forex,CHF,USD.CHF,"2025-11-12, 17:00:00",-1.633334,0.79770964,,1.3029,0,...
    if (
      section === "Trades" &&
      rowType === "Data" &&
      row[2] === "Order" &&
      (row[3] === "Stocks" || row[3] === "Forex")
    ) {
      const assetCategory = row[3];
      const currency = row[4] || "USD";
      const symbol = row[5];
      const dateTime = row[6];
      const quantityRaw = parseFloat(row[7] || "0");
      const tradePrice = parseFloat(row[8] || "0");
      const proceeds = parseFloat(row[10] || "0");
      const commFee = parseFloat(row[11] || "0");

      if (!symbol || !dateTime) continue;
      if (!Number.isFinite(quantityRaw) || quantityRaw === 0) continue;
      if (!Number.isFinite(proceeds) || proceeds === 0) continue;

      const side = proceeds < 0 ? "BUY" : "SELL";
      const type =
        assetCategory === "Forex"
          ? quantityRaw >= 0
            ? "FX BUY"
            : "FX SELL"
          : side;
      const quantity = Math.abs(quantityRaw);
      const totalAmount = Math.abs(proceeds);
      const commission = Number.isFinite(commFee) ? Math.abs(commFee) : 0;

      transactions.push({
        accountId,
        date: parseIbkrDateTime(dateTime),
        ticker: symbol,
        type,
        quantity: quantity.toString(),
        unitPrice: Number.isFinite(tradePrice) ? tradePrice.toString() : null,
        totalAmount: totalAmount.toString(),
        currency,
        commission: commission.toString(),
        fxRateOriginal: null,
      });
    }
  }

  return {
    baseCurrency,
    sourceType: "activity_csv",
    transactions,
  };
}

export function parseIBKR(
  content: string,
  accountId: string
): ParsedIbkrImport {
  const trimmed = content.trimStart();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    return parseIbkrFromJson(content, accountId);
  }
  return parseIbkrFromActivityCsv(content, accountId);
}

// Backward compatibility with existing callers.
export function parseIBKRJSON(
  jsonContent: string,
  accountId: string
): NewTransaction[] {
  return parseIbkrFromJson(jsonContent, accountId).transactions;
}

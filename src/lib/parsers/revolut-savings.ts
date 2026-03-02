import { parse } from "csv-parse/sync";
import type { NewBankingTransaction, NewTransaction } from "@/db/schema";

interface RawSavingsRow {
  Date: string;
  Description: string;
  "Value, USD": string;
  "Value, EUR": string;
  "FX Rate": string;
  "Price per share": string;
  "Quantity of shares": string;
}

// European number format: "0,33" → 0.33, "-0,01" → -0.01
function parseEuropeanNumber(raw: string): number {
  if (!raw || raw.trim() === "") return 0;
  return parseFloat(raw.replace(",", ".")) || 0;
}

// "DD/MM/YYYY, HH:mm:ss" → Date
function parseSavingsDate(raw: string): Date {
  const [datePart, timePart] = raw.split(", ");
  const [day, month, year] = datePart.split("/");
  return new Date(`${year}-${month}-${day}T${timePart}`);
}

// Extract ISIN from description: "BUY USD Class R IE000H9J0QX4" → "IE000H9J0QX4"
function extractISIN(desc: string): string | null {
  const match = desc.match(/\b([A-Z]{2}[A-Z0-9]{9}\d)\b/);
  return match ? match[1] : null;
}

// Extract currency from description: "BUY USD Class R..." → "USD"
function extractCurrency(desc: string): string {
  const match = desc.match(/\b(USD|EUR|GBP|CHF)\b/);
  return match ? match[1] : "USD";
}

// Determine if this is a BUY/SELL (investment) or banking transaction
function classifyRow(desc: string): "investment" | "banking" {
  if (desc.startsWith("BUY ") || desc.startsWith("SELL ")) {
    return "investment";
  }
  return "banking";
}

export interface RevolutSavingsResult {
  bankingTxns: NewBankingTransaction[];
  investmentTxns: NewTransaction[];
}

export function parseRevolutSavingsCSV(
  csvContent: string,
  opts: {
    bankingAccountId: string;
    investmentAccountId: string;
  }
): RevolutSavingsResult {
  const records: RawSavingsRow[] = parse(csvContent, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
  });

  const bankingTxns: NewBankingTransaction[] = [];
  const investmentTxns: NewTransaction[] = [];

  for (const row of records) {
    if (!row.Date) continue;

    const date = parseSavingsDate(row.Date);
    const valueUSD = parseEuropeanNumber(row["Value, USD"]);
    const valueEUR = parseEuropeanNumber(row["Value, EUR"]);
    const fxRate = parseFloat(row["FX Rate"]) || null;
    const pricePerShare = parseFloat(row["Price per share"]) || null;
    const quantity = parseEuropeanNumber(row["Quantity of shares"]);
    const desc = row.Description;
    const currency = extractCurrency(desc);

    const classification = classifyRow(desc);

    if (classification === "investment") {
      // BUY/SELL → goes to transactions table
      const isin = extractISIN(desc);
      const isBuy = desc.startsWith("BUY ");
      const type = isBuy ? "BUY" : "SELL";

      investmentTxns.push({
        accountId: opts.investmentAccountId,
        date,
        ticker: isin,
        type,
        quantity: Math.abs(quantity).toString(),
        unitPrice: pricePerShare?.toString() ?? null,
        totalAmount: Math.abs(valueUSD).toString(),
        currency,
        commission: "0",
        fxRateOriginal: fxRate?.toString() ?? null,
      });
    } else {
      // Interest PAID, Service Fee Charged, Interest Reinvested, Withdrawn
      let category: string | null = null;
      if (desc.includes("Interest PAID")) {
        category = "Income // Interest";
      } else if (desc.includes("Service Fee Charged")) {
        category = "Finances // Fees";
      } else if (desc.includes("Interest Reinvested")) {
        category = "Income // Interest Reinvested";
      } else if (desc.includes("Withdrawn")) {
        category = "Finances // Transfers other";
      }

      bankingTxns.push({
        accountId: opts.bankingAccountId,
        date,
        completedDate: date,
        description: desc,
        amount: valueUSD.toFixed(4),
        commission: "0",
        currency,
        balance: null,
        status: "completed",
        category,
        merchant: null,
        transferType: desc.includes("Withdrawn") ? "internal" : null,
        linkedAccountType: desc.includes("Withdrawn") ? "checking" : null,
        originalType: null,
        originalProduct: "Savings",
      });
    }
  }

  return { bankingTxns, investmentTxns };
}

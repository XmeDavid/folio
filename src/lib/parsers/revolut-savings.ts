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

/**
 * The savings CSV can have multiple sections with different headers
 * (e.g. USD section with 7 cols, then EUR section with 5 cols).
 * We split on header lines and parse each section separately.
 */
function splitSections(csvContent: string): { header: string; body: string }[] {
  const lines = csvContent.split("\n");
  const sections: { header: string; body: string }[] = [];
  let currentHeader = "";
  let currentLines: string[] = [];

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    // Detect header lines (start with "Date,Description")
    if (trimmed.startsWith("Date,Description")) {
      if (currentHeader && currentLines.length > 0) {
        sections.push({ header: currentHeader, body: currentLines.join("\n") });
      }
      currentHeader = trimmed;
      currentLines = [];
    } else if (currentHeader) {
      currentLines.push(line);
    }
  }

  if (currentHeader && currentLines.length > 0) {
    sections.push({ header: currentHeader, body: currentLines.join("\n") });
  }

  return sections;
}

interface ParsedSavingsRow {
  date: Date;
  description: string;
  value: number;
  currency: string;
  fxRate: number | null;
  pricePerShare: number | null;
  quantity: number;
}

function parseSection(header: string, body: string): ParsedSavingsRow[] {
  const fullCsv = header + "\n" + body;
  const records: Record<string, string>[] = parse(fullCsv, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
    relax_column_count: true,
  });

  const rows: ParsedSavingsRow[] = [];

  for (const row of records) {
    if (!row.Date) continue;

    const desc = row.Description || "";
    const currency = extractCurrency(desc);

    // Find the value column — could be "Value, USD", "Value, EUR", "Value, GBP", etc.
    let value = 0;
    for (const key of Object.keys(row)) {
      if (key.startsWith("Value,") || key.startsWith("Value, ")) {
        value = parseEuropeanNumber(row[key]);
        break;
      }
    }

    rows.push({
      date: parseSavingsDate(row.Date),
      description: desc,
      value,
      currency,
      fxRate: row["FX Rate"] ? parseFloat(row["FX Rate"]) || null : null,
      pricePerShare: row["Price per share"] ? parseFloat(row["Price per share"]) || null : null,
      quantity: parseEuropeanNumber(row["Quantity of shares"] || "0"),
    });
  }

  return rows;
}

export function parseRevolutSavingsCSV(
  csvContent: string,
  opts: {
    bankingAccountId: string;
    investmentAccountId: string;
  }
): RevolutSavingsResult {
  const sections = splitSections(csvContent);
  const allRows: ParsedSavingsRow[] = [];
  for (const section of sections) {
    allRows.push(...parseSection(section.header, section.body));
  }

  const bankingTxns: NewBankingTransaction[] = [];
  const investmentTxns: NewTransaction[] = [];

  for (const row of allRows) {
    const { date, description: desc, value, currency, fxRate, pricePerShare, quantity } = row;

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
        totalAmount: Math.abs(value).toString(),
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
        amount: value.toFixed(4),
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

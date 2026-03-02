import { parse } from "csv-parse/sync";
import type { NewBankingTransaction } from "@/db/schema";

interface RawPostFinanceRow {
  Date: string;
  "Type of transaction": string;
  "Notification text": string;
  "Credit in CHF": string;
  "Debit in CHF": string;
  Tag: string;
  Category: string;
}

interface PostFinanceMetadata {
  accountNumber: string | null;
  dateFrom: string | null;
  dateTo: string | null;
  currency: string;
}

function parseDate(raw: string): Date | null {
  // DD.MM.YYYY → Date
  const match = raw.match(/^(\d{2})\.(\d{2})\.(\d{4})$/);
  if (!match) return null;
  const [, day, month, year] = match;
  const d = new Date(`${year}-${month}-${day}T00:00:00`);
  if (isNaN(d.getTime())) return null;
  return d;
}

function extractMerchant(text: string): {
  merchant: string | null;
  transferType: string | null;
  linkedAccountType: string | null;
} {
  // "Debit to X" → merchant = X
  const debitMatch = text.match(/^Debit to (.+)$/i);
  if (debitMatch) {
    return { merchant: debitMatch[1].trim(), transferType: "external", linkedAccountType: null };
  }

  // "Credit from X" → merchant = X
  const creditMatch = text.match(/^Credit from (.+)$/i);
  if (creditMatch) {
    return { merchant: creditMatch[1].trim(), transferType: "external", linkedAccountType: null };
  }

  // Revolut transfers
  if (text.includes("Revolut**") || text.includes("Revolut*")) {
    return { merchant: "Revolut", transferType: "internal", linkedAccountType: "checking" };
  }

  // "Purchase/online shopping of DD.MM.YYYY, MERCHANT"
  const purchaseMatch = text.match(/Purchase\/online shopping of \d{2}\.\d{2}\.\d{4},\s*(.+)$/i);
  if (purchaseMatch) {
    const merchantName = purchaseMatch[1].trim();
    if (merchantName.includes("Revolut**") || merchantName.includes("Revolut*")) {
      return { merchant: "Revolut", transferType: "internal", linkedAccountType: "checking" };
    }
    return { merchant: merchantName, transferType: null, linkedAccountType: null };
  }

  // "TWINT purchase/service MERCHANT"
  const twintMatch = text.match(/TWINT purchase\/service\s+(.+)$/i);
  if (twintMatch) {
    return { merchant: twintMatch[1].trim(), transferType: null, linkedAccountType: null };
  }

  // "International payment (SEPA) to MERCHANT"
  const sepaMatch = text.match(/International payment \(SEPA\) to (.+)$/i);
  if (sepaMatch) {
    return { merchant: sepaMatch[1].trim(), transferType: "external", linkedAccountType: null };
  }

  return { merchant: null, transferType: null, linkedAccountType: null };
}

function parseMetadata(content: string): PostFinanceMetadata {
  const lines = content.split("\n");
  const meta: PostFinanceMetadata = {
    accountNumber: null,
    dateFrom: null,
    dateTo: null,
    currency: "CHF",
  };

  for (let i = 0; i < Math.min(lines.length, 6); i++) {
    const line = lines[i];
    const accountMatch = line.match(/Account:;="(.+?)"/);
    if (accountMatch) meta.accountNumber = accountMatch[1];

    const currencyMatch = line.match(/Currency:;="(.+?)"/);
    if (currencyMatch) meta.currency = currencyMatch[1];

    const dateFromMatch = line.match(/Date from:;="(.+?)"/);
    if (dateFromMatch) meta.dateFrom = dateFromMatch[1];

    const dateToMatch = line.match(/Date to:;="(.+?)"/);
    if (dateToMatch) meta.dateTo = dateToMatch[1];
  }

  return meta;
}

export function parsePostFinanceCSV(
  csvContent: string,
  accountId: string
): NewBankingTransaction[] {
  // Strip BOM and normalize line endings
  const normalized = csvContent.replace(/^\uFEFF/, "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");

  const meta = parseMetadata(normalized);

  // Find the header line (after metadata)
  const lines = normalized.split("\n");
  let headerIndex = -1;
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].startsWith("Date;Type of transaction;")) {
      headerIndex = i;
      break;
    }
  }
  if (headerIndex === -1) {
    throw new Error("Could not find PostFinance CSV header line");
  }

  const dataContent = lines.slice(headerIndex).join("\n");

  const records: RawPostFinanceRow[] = parse(dataContent, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
    delimiter: ";",
    relax_column_count: true,
  });

  const results: NewBankingTransaction[] = [];
  let runningBalance = 0;

  // Process in chronological order (reverse since CSV is newest-first)
  const sorted = [...records].reverse();

  for (const row of sorted) {
    if (!row.Date) continue;

    const date = parseDate(row.Date);
    if (!date) continue; // skip non-date rows (disclaimer footer, etc.)

    const credit = row["Credit in CHF"] ? parseFloat(row["Credit in CHF"]) : 0;
    const debit = row["Debit in CHF"] ? parseFloat(row["Debit in CHF"]) : 0;
    const amount = credit + debit; // debit is already negative

    runningBalance += amount;

    const { merchant, transferType, linkedAccountType } = extractMerchant(
      row["Notification text"] || ""
    );

    // Parse category "Parent // Sub" format
    let category = row.Category || null;

    results.push({
      accountId,
      date,
      completedDate: date,
      description: row["Notification text"] || row["Type of transaction"],
      amount: amount.toFixed(4),
      commission: "0",
      currency: meta.currency,
      balance: runningBalance.toFixed(4),
      status: "completed",
      category,
      merchant,
      transferType,
      linkedAccountType,
      originalType: row["Type of transaction"] || null,
      originalProduct: null,
    });
  }

  // Return in original (newest-first) order
  return results.reverse();
}

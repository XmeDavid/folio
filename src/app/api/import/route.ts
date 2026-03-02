import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { transactions, accounts } from "@/db/schema";
import { parseRevolutCSV } from "@/lib/parsers/revolut";
import { parseIBKR } from "@/lib/parsers/ibkr";
import { and, eq } from "drizzle-orm";
import type { NewTransaction } from "@/db/schema";

function normalizeNumLike(value: string | null | undefined): string {
  if (value === null || value === undefined || value === "") return "";
  const n = Number(value);
  return Number.isFinite(n) ? n.toString() : String(value).trim();
}

function normalizeScaled(value: string | null | undefined, scale: number): string {
  if (value === null || value === undefined || value === "") return "";
  const n = Number(value);
  if (!Number.isFinite(n)) return String(value).trim();
  return n.toFixed(scale);
}

function txFingerprint(tx: {
  date: Date;
  ticker: string | null;
  type: string;
  quantity: string | null;
  unitPrice: string | null;
  totalAmount: string | null | undefined;
  currency: string | null | undefined;
  commission: string | null;
  fxRateOriginal: string | null;
}, opts?: { dateGranularity?: "second" | "day" }): string {
  const dateKey =
    opts?.dateGranularity === "day"
      ? tx.date.toISOString().slice(0, 10)
      : tx.date.toISOString();
  return [
    dateKey,
    tx.ticker ?? "",
    tx.type,
    normalizeScaled(tx.quantity, 8),
    normalizeScaled(tx.unitPrice, 8),
    normalizeScaled(tx.totalAmount, 4),
    tx.currency ?? "",
    normalizeScaled(tx.commission, 4),
    normalizeScaled(tx.fxRateOriginal, 6),
  ].join("|");
}

export async function POST(req: NextRequest) {
  const formData = await req.formData();
  const file = formData.get("file") as File | null;
  const broker = formData.get("broker") as string;
  const accountName = formData.get("accountName") as string;

  if (!file || !broker) {
    return NextResponse.json(
      { error: "file and broker are required" },
      { status: 400 }
    );
  }

  const content = await file.text();

  const targetAccountName = (accountName?.trim() || `${broker} Account`).trim();

  const existingAccount = await db
    .select()
    .from(accounts)
    .where(and(eq(accounts.broker, broker), eq(accounts.name, targetAccountName)))
    .limit(1);

  const account =
    existingAccount[0] ??
    (
      await db
        .insert(accounts)
        .values({
          name: targetAccountName,
          broker,
          baseCurrency: "USD",
        })
        .returning()
    )[0];

  let parsed: NewTransaction[];
  let detectedBaseCurrency: string | null = null;
  const fingerprintDateGranularity = broker === "IBKR" ? "day" : "second";
  if (broker === "Revolut") {
    parsed = parseRevolutCSV(content, account.id);
  } else if (broker === "IBKR") {
    const ibkr = parseIBKR(content, account.id);
    parsed = ibkr.transactions;
    detectedBaseCurrency = ibkr.baseCurrency;
  } else {
    return NextResponse.json(
      { error: "Unsupported broker. Use 'Revolut' or 'IBKR'." },
      { status: 400 }
    );
  }

  // Deduplicate against existing imported rows in this account (same trade fingerprint),
  // and also deduplicate duplicates within the uploaded file.
  let existingByFingerprint = new Set<string>();
  if (parsed.length > 0) {
    const existingRows = await db
      .select()
      .from(transactions)
      .where(eq(transactions.accountId, account.id));

    existingByFingerprint = new Set(
      existingRows.map((row) =>
        txFingerprint({
          date: row.date,
          ticker: row.ticker,
          type: row.type,
          quantity: row.quantity,
          unitPrice: row.unitPrice,
          totalAmount: row.totalAmount,
          currency: row.currency,
          commission: row.commission ?? null,
          fxRateOriginal: row.fxRateOriginal ?? null,
        }, { dateGranularity: fingerprintDateGranularity })
      )
    );
  }

  const inFile = new Set<string>();
  const uniqueParsed: NewTransaction[] = [];
  let duplicatesSkipped = 0;
  for (const tx of parsed) {
    const fp = txFingerprint({
      date: tx.date,
      ticker: tx.ticker ?? null,
      type: tx.type,
      quantity: tx.quantity ?? null,
      unitPrice: tx.unitPrice ?? null,
      totalAmount: tx.totalAmount,
      currency: tx.currency,
      commission: tx.commission ?? null,
      fxRateOriginal: tx.fxRateOriginal ?? null,
    }, { dateGranularity: fingerprintDateGranularity });

    if (existingByFingerprint.has(fp) || inFile.has(fp)) {
      duplicatesSkipped += 1;
      continue;
    }
    inFile.add(fp);
    uniqueParsed.push(tx);
  }

  const BATCH_SIZE = 100;
  let inserted = 0;
  for (let i = 0; i < uniqueParsed.length; i += BATCH_SIZE) {
    const batch = uniqueParsed.slice(i, i + BATCH_SIZE);
    await db.insert(transactions).values(batch);
    inserted += batch.length;
  }

  if (detectedBaseCurrency) {
    await db
      .update(accounts)
      .set({ baseCurrency: detectedBaseCurrency })
      .where(eq(accounts.id, account.id));
  }

  return NextResponse.json({
    accountId: account.id,
    accountName: account.name,
    totalParsed: parsed.length,
    duplicatesSkipped,
    transactionsImported: inserted,
  });
}

import { NextRequest, NextResponse } from "next/server";
import { db } from "@/db";
import { transactions, accounts, bankingTransactions } from "@/db/schema";
import { parseRevolutCSV } from "@/lib/parsers/revolut";
import { parseIBKR } from "@/lib/parsers/ibkr";
import { parsePostFinanceCSV } from "@/lib/parsers/postfinance";
import { parseRevolutBankingCSV } from "@/lib/parsers/revolut-banking";
import { parseRevolutSavingsCSV } from "@/lib/parsers/revolut-savings";
import { and, eq } from "drizzle-orm";
import type { NewTransaction, NewBankingTransaction } from "@/db/schema";
import { reconcileTransfers } from "@/lib/banking/reconcile-transfers";

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

function bankingTxFingerprint(tx: {
  date: Date;
  description: string;
  amount: string;
  currency: string;
  commission: string | null;
}): string {
  return [
    tx.date.toISOString(),
    tx.description,
    normalizeScaled(tx.amount, 4),
    tx.currency,
    normalizeScaled(tx.commission, 4),
  ].join("|");
}

async function getOrCreateAccount(
  broker: string,
  name: string,
  type: string = "investment",
  baseCurrency: string = "USD"
) {
  const existing = await db
    .select()
    .from(accounts)
    .where(and(eq(accounts.broker, broker), eq(accounts.name, name)))
    .limit(1);

  if (existing[0]) return existing[0];

  const [created] = await db
    .insert(accounts)
    .values({ name, broker, type, baseCurrency })
    .returning();
  return created;
}

async function dedupAndInsertTransactions(
  parsed: NewTransaction[],
  accountId: string,
  dateGranularity: "second" | "day"
): Promise<{ inserted: number; duplicatesSkipped: number }> {
  let existingByFingerprint = new Set<string>();
  if (parsed.length > 0) {
    const existingRows = await db
      .select()
      .from(transactions)
      .where(eq(transactions.accountId, accountId));

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
        }, { dateGranularity })
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
    }, { dateGranularity });

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

  return { inserted, duplicatesSkipped };
}

async function dedupAndInsertBankingTransactions(
  parsed: NewBankingTransaction[],
  accountIds: string[]
): Promise<{ inserted: number; duplicatesSkipped: number }> {
  let existingByFingerprint = new Set<string>();
  if (parsed.length > 0) {
    const existingRows = await db
      .select()
      .from(bankingTransactions);

    // Only check fingerprints for relevant accounts
    const accountIdSet = new Set(accountIds);
    existingByFingerprint = new Set(
      existingRows
        .filter((row) => accountIdSet.has(row.accountId))
        .map((row) =>
          bankingTxFingerprint({
            date: row.date,
            description: row.description,
            amount: row.amount,
            currency: row.currency,
            commission: row.commission ?? null,
          })
        )
    );
  }

  const inFile = new Set<string>();
  const uniqueParsed: NewBankingTransaction[] = [];
  let duplicatesSkipped = 0;
  for (const tx of parsed) {
    const fp = bankingTxFingerprint({
      date: tx.date,
      description: tx.description,
      amount: tx.amount,
      currency: tx.currency,
      commission: tx.commission ?? null,
    });

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
    await db.insert(bankingTransactions).values(batch);
    inserted += batch.length;
  }

  return { inserted, duplicatesSkipped };
}

export async function POST(req: NextRequest) {
  try {
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

  // --- Investment sources (existing) ---
  if (broker === "Revolut" || broker === "IBKR") {
    const targetAccountName = (accountName?.trim() || `${broker} Account`).trim();
    const account = await getOrCreateAccount(broker, targetAccountName, "investment", "USD");

    let parsed: NewTransaction[];
    let detectedBaseCurrency: string | null = null;
    const fingerprintDateGranularity = broker === "IBKR" ? "day" : "second";

    if (broker === "Revolut") {
      parsed = parseRevolutCSV(content, account.id);
    } else {
      const ibkr = parseIBKR(content, account.id);
      parsed = ibkr.transactions;
      detectedBaseCurrency = ibkr.baseCurrency;
    }

    const { inserted, duplicatesSkipped } = await dedupAndInsertTransactions(
      parsed, account.id, fingerprintDateGranularity
    );

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

  // --- Revolut Banking (account-statement.csv) ---
  if (broker === "Revolut-Banking") {
    const checkingAccount = await getOrCreateAccount("Revolut", "Revolut Current", "checking", "CHF");
    const savingsAccount = await getOrCreateAccount("Revolut", "Revolut Savings", "savings", "CHF");

    const parsed = parseRevolutBankingCSV(content, {
      checking: checkingAccount.id,
      savings: savingsAccount.id,
    });

    const { inserted, duplicatesSkipped } = await dedupAndInsertBankingTransactions(
      parsed, [checkingAccount.id, savingsAccount.id]
    );

    if (inserted > 0) await reconcileTransfers();

    return NextResponse.json({
      accountId: checkingAccount.id,
      accountName: "Revolut Current + Savings",
      totalParsed: parsed.length,
      duplicatesSkipped,
      transactionsImported: inserted,
    });
  }

  // --- Revolut Savings (savings-statement.csv) ---
  if (broker === "Revolut-Savings") {
    const savingsAccount = await getOrCreateAccount("Revolut", "Revolut Money Market", "savings", "USD");
    const investmentAccount = await getOrCreateAccount("Revolut", "Revolut Money Market Inv", "investment", "USD");

    const { bankingTxns, investmentTxns } = parseRevolutSavingsCSV(content, {
      bankingAccountId: savingsAccount.id,
      investmentAccountId: investmentAccount.id,
    });

    const bankingResult = await dedupAndInsertBankingTransactions(
      bankingTxns, [savingsAccount.id]
    );
    const investmentResult = await dedupAndInsertTransactions(
      investmentTxns, investmentAccount.id, "second"
    );

    if (bankingResult.inserted > 0) await reconcileTransfers();

    return NextResponse.json({
      accountId: savingsAccount.id,
      accountName: "Revolut Money Market",
      totalParsed: bankingTxns.length + investmentTxns.length,
      duplicatesSkipped: bankingResult.duplicatesSkipped + investmentResult.duplicatesSkipped,
      transactionsImported: bankingResult.inserted + investmentResult.inserted,
      detail: {
        bankingTxns: bankingResult.inserted,
        investmentTxns: investmentResult.inserted,
      },
    });
  }

  // --- PostFinance ---
  if (broker === "PostFinance") {
    const targetAccountName = (accountName?.trim() || "PostFinance").trim();
    const account = await getOrCreateAccount("PostFinance", targetAccountName, "checking", "CHF");

    const parsed = parsePostFinanceCSV(content, account.id);

    const { inserted, duplicatesSkipped } = await dedupAndInsertBankingTransactions(
      parsed, [account.id]
    );

    if (inserted > 0) await reconcileTransfers();

    return NextResponse.json({
      accountId: account.id,
      accountName: account.name,
      totalParsed: parsed.length,
      duplicatesSkipped,
      transactionsImported: inserted,
    });
  }

  return NextResponse.json(
    { error: "Unsupported source. Use 'Revolut', 'IBKR', 'Revolut-Banking', 'Revolut-Savings', or 'PostFinance'." },
    { status: 400 }
  );
  } catch (err) {
    console.error("[import] Error:", err);
    return NextResponse.json(
      { error: err instanceof Error ? err.message : String(err) },
      { status: 500 }
    );
  }
}

import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";
import { existsSync, readFileSync } from "fs";
import { resolve } from "path";
import { accounts, transactions } from "../db/schema";
import { parseRevolutCSV } from "../lib/parsers/revolut";
import { parseIBKR } from "../lib/parsers/ibkr";
import { eq } from "drizzle-orm";

async function seed() {
  const client = postgres(process.env.DATABASE_URL!);
  const db = drizzle(client);

  console.log("Clearing existing data...");
  await db.delete(transactions);
  await db.delete(accounts);

  console.log("Creating accounts...");
  const [revolutAccount] = await db
    .insert(accounts)
    .values({ name: "Revolut", broker: "Revolut", baseCurrency: "USD" })
    .returning();

  const [ibkrAccount] = await db
    .insert(accounts)
    .values({ name: "IBKR", broker: "IBKR", baseCurrency: "USD" })
    .returning();

  console.log(`  Revolut account: ${revolutAccount.id}`);
  console.log(`  IBKR account:    ${ibkrAccount.id}`);

  const revolutCsvPath = resolve(__dirname, "../../data/revolut_feb.csv");
  const revolutLegacyPath = resolve(__dirname, "../../data/revolut_export.csv");
  const revolutPath = existsSync(revolutCsvPath) ? revolutCsvPath : revolutLegacyPath;

  const revolutCSV = readFileSync(revolutPath, "utf-8");
  const revolutTxns = parseRevolutCSV(revolutCSV, revolutAccount.id);
  const withFees = revolutTxns.filter((t) => parseFloat(t.commission || "0") > 0);
  console.log(
    `Parsed ${revolutTxns.length} Revolut transactions (${withFees.length} with implicit fees extracted)`
  );

  const BATCH_SIZE = 100;
  for (let i = 0; i < revolutTxns.length; i += BATCH_SIZE) {
    const batch = revolutTxns.slice(i, i + BATCH_SIZE);
    await db.insert(transactions).values(batch);
    console.log(
      `  Inserted Revolut batch ${Math.floor(i / BATCH_SIZE) + 1}/${Math.ceil(revolutTxns.length / BATCH_SIZE)}`
    );
  }

  // Revolut Robo Advisory account
  const roboCsvPath = resolve(__dirname, "../../data/rev_robot_feb.csv");
  if (existsSync(roboCsvPath)) {
    const [roboAccount] = await db
      .insert(accounts)
      .values({ name: "Revolut Robo Advisory", broker: "Revolut", baseCurrency: "EUR" })
      .returning();
    console.log(`  Robo account:    ${roboAccount.id}`);

    const roboCSV = readFileSync(roboCsvPath, "utf-8");
    const roboTxns = parseRevolutCSV(roboCSV, roboAccount.id);
    console.log(`Parsed ${roboTxns.length} Revolut Robo transactions`);

    for (let i = 0; i < roboTxns.length; i += BATCH_SIZE) {
      const batch = roboTxns.slice(i, i + BATCH_SIZE);
      await db.insert(transactions).values(batch);
    }
    console.log("  Inserted Revolut Robo transactions");
  }

  const ibkrActivityCsvPath = resolve(__dirname, "../../data/ibkr_feb.csv");
  const ibkrLegacyActivityPath = resolve(
    __dirname,
    "../../data/ibkr_feb.csv"
  );
  const ibkrJsonPath = resolve(__dirname, "../../data/ibkr_transactions.json");
  const ibkrPath = existsSync(ibkrActivityCsvPath)
    ? ibkrActivityCsvPath
    : existsSync(ibkrLegacyActivityPath)
      ? ibkrLegacyActivityPath
      : ibkrJsonPath;

  const ibkrContent = readFileSync(ibkrPath, "utf-8");
  const ibkrParsed = parseIBKR(ibkrContent, ibkrAccount.id);
  console.log(
    `Parsed ${ibkrParsed.transactions.length} IBKR transactions from ${ibkrPath.split("/").pop()}`
  );
  await db.insert(transactions).values(ibkrParsed.transactions);
  await db
    .update(accounts)
    .set({ baseCurrency: ibkrParsed.baseCurrency || "USD" })
    .where(eq(accounts.id, ibkrAccount.id));
  console.log("  Inserted IBKR transactions");

  console.log("Seed complete!");
  await client.end();
}

seed().catch((err) => {
  console.error("Seed failed:", err);
  process.exit(1);
});

import { describe, it, expect } from "vitest";
import fs from "fs";
import path from "path";
import { parseRevolutSavingsCSV } from "./revolut-savings";

const DATA_PATH = path.resolve(__dirname, "../../../data/savings-statement.csv");
const BANKING_ACCOUNT_ID = "test-banking-id";
const INVESTMENT_ACCOUNT_ID = "test-investment-id";

describe("Revolut Savings parser", () => {
  const csv = fs.readFileSync(DATA_PATH, "utf-8");

  it("parses without throwing", () => {
    expect(() =>
      parseRevolutSavingsCSV(csv, {
        bankingAccountId: BANKING_ACCOUNT_ID,
        investmentAccountId: INVESTMENT_ACCOUNT_ID,
      })
    ).not.toThrow();
  });

  it("returns both banking and investment transactions", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });
    expect(result.bankingTxns.length).toBeGreaterThan(0);
    expect(result.investmentTxns.length).toBeGreaterThan(0);
  });

  it("total row count is approximately 1610", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });
    const total = result.bankingTxns.length + result.investmentTxns.length;
    // Allow some tolerance for skipped rows
    expect(total).toBeGreaterThan(1500);
    expect(total).toBeLessThan(1700);
  });

  it("handles multi-section CSV (USD, GBP, EUR)", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    const allTxns = [...result.bankingTxns, ...result.investmentTxns];
    const currencies = new Set(allTxns.map((t) => t.currency));
    expect(currencies.has("USD")).toBe(true);
    expect(currencies.has("EUR")).toBe(true);
  });

  it("all dates are valid", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    for (const tx of result.bankingTxns) {
      expect(tx.date).toBeInstanceOf(Date);
      expect(isNaN(tx.date.getTime())).toBe(false);
    }
    for (const tx of result.investmentTxns) {
      expect(tx.date).toBeInstanceOf(Date);
      expect(isNaN(tx.date.getTime())).toBe(false);
    }
  });

  it("all amounts are valid numbers", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    for (const tx of result.bankingTxns) {
      const amount = parseFloat(tx.amount);
      expect(Number.isFinite(amount)).toBe(true);
    }
    for (const tx of result.investmentTxns) {
      const amount = parseFloat(tx.totalAmount);
      expect(Number.isFinite(amount)).toBe(true);
    }
  });

  it("BUY/SELL transactions have ISIN tickers", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    for (const tx of result.investmentTxns) {
      expect(tx.ticker).toMatch(/^[A-Z]{2}[A-Z0-9]{9}\d$/);
      expect(["BUY", "SELL"]).toContain(tx.type);
    }
  });

  it("banking transactions have correct categories", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    const interestTxns = result.bankingTxns.filter(
      (t) => t.category === "Income // Interest"
    );
    const feeTxns = result.bankingTxns.filter(
      (t) => t.category === "Finances // Fees"
    );

    expect(interestTxns.length).toBeGreaterThan(0);
    expect(feeTxns.length).toBeGreaterThan(0);
  });

  it("european number format is parsed correctly", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    // No amounts should be exactly 0 except possibly some edge cases
    const nonZeroInvestment = result.investmentTxns.filter(
      (t) => parseFloat(t.totalAmount) !== 0
    );
    expect(nonZeroInvestment.length).toBeGreaterThan(100);

    // Check that quantities are parsed (e.g. "0,33" → 0.33, not 0)
    const withQuantity = result.investmentTxns.filter(
      (t) => t.quantity && parseFloat(t.quantity) > 0
    );
    expect(withQuantity.length).toBeGreaterThan(100);
  });

  it("assigns correct account IDs", () => {
    const result = parseRevolutSavingsCSV(csv, {
      bankingAccountId: BANKING_ACCOUNT_ID,
      investmentAccountId: INVESTMENT_ACCOUNT_ID,
    });

    for (const tx of result.bankingTxns) {
      expect(tx.accountId).toBe(BANKING_ACCOUNT_ID);
    }
    for (const tx of result.investmentTxns) {
      expect(tx.accountId).toBe(INVESTMENT_ACCOUNT_ID);
    }
  });
});

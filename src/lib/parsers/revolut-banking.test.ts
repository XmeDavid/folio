import { describe, it, expect } from "vitest";
import fs from "fs";
import path from "path";
import { parseRevolutBankingCSV } from "./revolut-banking";

const DATA_PATH = path.resolve(__dirname, "../../../data/account-statement.csv");
const CHECKING_ID = "test-checking-id";
const SAVINGS_ID = "test-savings-id";

describe("Revolut Banking parser", () => {
  const csv = fs.readFileSync(DATA_PATH, "utf-8");

  it("parses without throwing", () => {
    expect(() =>
      parseRevolutBankingCSV(csv, { checking: CHECKING_ID, savings: SAVINGS_ID })
    ).not.toThrow();
  });

  it("skips Investimentos and Depósito rows", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    // Original file has ~4693 rows, but Investimentos and Depósito should be excluded
    // Actual + Poupanças rows should be ~3800
    expect(result.length).toBeGreaterThan(3000);
    expect(result.length).toBeLessThan(4500);

    for (const tx of result) {
      expect(tx.originalProduct).not.toBe("Investimentos");
      expect(tx.originalProduct).not.toBe("Depósito");
    }
  });

  it("assigns correct account IDs by product type", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    const checking = result.filter((r) => r.accountId === CHECKING_ID);
    const savings = result.filter((r) => r.accountId === SAVINGS_ID);

    expect(checking.length).toBeGreaterThan(0);
    expect(savings.length).toBeGreaterThan(0);

    for (const tx of checking) {
      expect(tx.originalProduct).toBe("Atual");
    }
    for (const tx of savings) {
      expect(tx.originalProduct).toBe("Poupanças");
    }
  });

  it("all dates are valid", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    for (const tx of result) {
      expect(tx.date).toBeInstanceOf(Date);
      expect(isNaN(tx.date.getTime())).toBe(false);
    }
  });

  it("all amounts are valid numbers", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    for (const tx of result) {
      const amount = parseFloat(tx.amount);
      expect(Number.isFinite(amount)).toBe(true);
    }
  });

  it("maps status correctly", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    const statuses = new Set(result.map((r) => r.status));
    // Should only have completed, reversed, pending
    for (const s of statuses) {
      expect(["completed", "reversed", "pending"]).toContain(s);
    }

    // Should have at least completed and reversed (from CONCLUÍDA and REVERTIDA)
    expect(statuses.has("completed")).toBe(true);
    expect(statuses.has("reversed")).toBe(true);
  });

  it("categorizes card payments with merchant", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    const cardPayments = result.filter(
      (r) => r.originalType === "Pagamento com cartão"
    );
    expect(cardPayments.length).toBeGreaterThan(0);

    // Card payments should have a merchant (the description)
    for (const tx of cardPayments) {
      expect(tx.merchant).not.toBeNull();
      expect(tx.merchant).not.toBe("");
    }
  });

  it("categorizes FX conversions", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    const fx = result.filter((r) => r.originalType === "Câmbio");
    expect(fx.length).toBeGreaterThan(0);

    for (const tx of fx) {
      expect(tx.category).toBe("Finances // FX Conversion");
    }
  });

  it("handles multiple currencies", () => {
    const result = parseRevolutBankingCSV(csv, {
      checking: CHECKING_ID,
      savings: SAVINGS_ID,
    });

    const currencies = new Set(result.map((r) => r.currency));
    expect(currencies.size).toBeGreaterThan(1);
    expect(currencies.has("CHF")).toBe(true);
  });
});

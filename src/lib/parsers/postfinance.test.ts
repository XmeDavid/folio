import { describe, it, expect } from "vitest";
import fs from "fs";
import path from "path";
import { parsePostFinanceCSV } from "./postfinance";

const DATA_PATH = path.resolve(__dirname, "../../../data/export_transactions_postfinance.csv");
const ACCOUNT_ID = "test-account-id";

describe("PostFinance parser", () => {
  const csv = fs.readFileSync(DATA_PATH, "utf-8");

  it("parses without throwing", () => {
    expect(() => parsePostFinanceCSV(csv, ACCOUNT_ID)).not.toThrow();
  });

  it("returns 522 valid transactions", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    expect(result.length).toBe(522);
  });

  it("skips disclaimer/footer lines", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    const descriptions = result.map((r) => r.description);
    expect(descriptions).not.toContain("Disclaimer:");
    for (const d of descriptions) {
      expect(d).not.toMatch(/PostFinance is not responsible/);
    }
  });

  it("all dates are valid Date objects", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    for (const tx of result) {
      expect(tx.date).toBeInstanceOf(Date);
      expect(isNaN(tx.date.getTime())).toBe(false);
      expect(tx.completedDate).toBeInstanceOf(Date);
      expect(isNaN(tx.completedDate!.getTime())).toBe(false);
    }
  });

  it("all amounts are valid numbers", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    for (const tx of result) {
      const amount = parseFloat(tx.amount);
      expect(Number.isFinite(amount)).toBe(true);
    }
  });

  it("currency is CHF for all rows", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    for (const tx of result) {
      expect(tx.currency).toBe("CHF");
    }
  });

  it("has categories on most rows", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    const withCategory = result.filter((r) => r.category);
    expect(withCategory.length).toBeGreaterThan(400);
  });

  it("detects Revolut transfers as internal", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    const revolut = result.filter((r) => r.merchant === "Revolut");
    expect(revolut.length).toBeGreaterThan(0);
    for (const tx of revolut) {
      expect(tx.transferType).toBe("internal");
    }
  });

  it("extracts merchants from 'Debit to X' pattern", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    const debits = result.filter(
      (r) => r.description.startsWith("Debit to ") && r.merchant
    );
    expect(debits.length).toBeGreaterThan(0);
    for (const tx of debits) {
      expect(tx.merchant).not.toBe("");
      expect(tx.merchant).not.toMatch(/^Debit to/);
    }
  });

  it("extracts merchants from 'Credit from X' pattern", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    const credits = result.filter(
      (r) => r.description.startsWith("Credit from ") && r.merchant
    );
    expect(credits.length).toBeGreaterThan(0);
    for (const tx of credits) {
      expect(tx.merchant).not.toBe("");
      expect(tx.merchant).not.toMatch(/^Credit from/);
    }
  });

  it("balance field is set on all rows", () => {
    const result = parsePostFinanceCSV(csv, ACCOUNT_ID);
    for (const tx of result) {
      expect(tx.balance).not.toBeNull();
      const balance = parseFloat(tx.balance!);
      expect(Number.isFinite(balance)).toBe(true);
    }
  });

  it("handles BOM and CRLF line endings", () => {
    // Simulate BOM + CRLF
    const withBOM = "\uFEFF" + csv.replace(/\n/g, "\r\n");
    expect(() => parsePostFinanceCSV(withBOM, ACCOUNT_ID)).not.toThrow();
    const result = parsePostFinanceCSV(withBOM, ACCOUNT_ID);
    expect(result.length).toBe(522);
  });
});

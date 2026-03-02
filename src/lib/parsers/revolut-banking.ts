import { parse } from "csv-parse/sync";
import type { NewBankingTransaction } from "@/db/schema";

interface RawRevolutBankingRow {
  Tipo: string;
  Produto: string;
  "Data de início": string;
  "Data de Conclusão": string;
  "Descrição": string;
  Montante: string;
  "Comissão": string;
  Moeda: string;
  Estado: string;
  Saldo: string;
}

// Products to skip — already handled by existing trading CSV or trivial
const SKIP_PRODUCTS = new Set(["Investimentos", "Depósito"]);

// Map Portuguese status to English
function mapStatus(estado: string): string {
  switch (estado) {
    case "CONCLUÍDA":
      return "completed";
    case "REVERTIDA":
      return "reversed";
    default:
      return "pending";
  }
}

// Map Produto to account type
function mapProduct(produto: string): "checking" | "savings" {
  if (produto === "Poupanças") return "savings";
  return "checking"; // Atual
}

// Common merchant keyword → category mapping
const MERCHANT_CATEGORIES: [RegExp, string][] = [
  [/\b(migros|coop|lidl|aldi|denner)\b/i, "Shopping // Supermarkets"],
  [/\b(sbb|tpg|bvb|zvv|vbl)\b/i, "Mobility // Public transport"],
  [/\b(uber|bolt|taxi)\b/i, "Mobility // Private transport"],
  [/\b(mcdonald|burger king|starbucks|subway)\b/i, "Leisure // Food services"],
  [/\b(netflix|spotify|disney|youtube|apple\.com\/bill|google)\b/i, "Residing // Communication"],
  [/\b(amazon|aliexpress|zalando|galaxus)\b/i, "Shopping // Online shopping"],
  [/\b(h&m|zara|uniqlo|c&a)\b/i, "Shopping // Fashion"],
  [/\b(ikea)\b/i, "Residing // Furniture"],
  [/\b(airbnb|booking\.com|hostel|hotel)\b/i, "Leisure // Travel & Experience"],
  [/\b(grab)\b/i, "Mobility // Private transport"],
  [/\b(hetzner|digitalocean|aws|azure)\b/i, "Residing // Communication"],
  [/\b(wolt|uber eats|eat\.ch|just eat)\b/i, "Leisure // Food services"],
  [/\b(swisscom|sunrise|salt)\b/i, "Residing // Communication"],
  [/\b(swiss ?life|axa|helvetia|atupri|css)\b/i, "Finances // Insurance"],
  [/\b(pharmacy|apotheke|amavita)\b/i, "Living // Healthcare"],
];

function guessCategoryByMerchant(merchant: string): string | null {
  for (const [re, cat] of MERCHANT_CATEGORIES) {
    if (re.test(merchant)) return cat;
  }
  return null;
}

interface CategorizeResult {
  category: string | null;
  merchant: string | null;
  transferType: string | null;
  linkedAccountType: string | null;
}

function categorize(tipo: string, descricao: string, produto: string): CategorizeResult {
  const result: CategorizeResult = {
    category: null,
    merchant: null,
    transferType: null,
    linkedAccountType: null,
  };

  // FX conversion
  if (tipo === "Câmbio" || descricao.includes("Conversão cambial") || descricao.includes("Conversão cambial")) {
    result.category = "Finances // FX Conversion";
    // If it's a transfer to investment/savings sub-account
    if (descricao.includes("Transfer to Revolut")) {
      result.transferType = "internal";
      result.linkedAccountType = "investment";
    }
    return result;
  }

  // Card payment → extract merchant
  if (tipo === "Pagamento com cartão") {
    result.merchant = descricao;
    result.category = guessCategoryByMerchant(descricao);
    return result;
  }

  // Transfer to investment account
  if (
    descricao.includes("Para a conta de investimento") ||
    descricao.includes("To investment account") ||
    tipo === "TRADE"
  ) {
    result.transferType = "internal";
    result.linkedAccountType = "investment";
    result.category = "Finances // Transfers other";
    return result;
  }

  // Savings sub-account loading
  if (
    descricao.includes("Carregamento de subconta") ||
    descricao.includes("Levantamento de Subconta") ||
    descricao.includes("Savings Vault") ||
    descricao.includes("savings") ||
    descricao.includes("cofre de poupanças")
  ) {
    result.transferType = "internal";
    result.linkedAccountType = "savings";
    result.category = "Finances // Transfers other";
    return result;
  }

  // ATM withdrawal
  if (tipo === "ATM" || descricao.includes("Levantamento de numerário")) {
    result.category = "Finances // Cash withdrawals";
    return result;
  }

  // Fee
  if (tipo === "Taxa") {
    result.category = "Finances // Fees";
    return result;
  }

  // FEE_REVERSAL
  if (tipo === "FEE_REVERSAL") {
    result.category = "Finances // Fees";
    return result;
  }

  // Top-up / loading (incoming)
  if (tipo === "Carregamento" || tipo === "Cobrança") {
    result.category = "Income";
    result.transferType = "external";
    return result;
  }

  // Revolut-to-Revolut payment
  if (tipo === "Pagamento Revolut") {
    result.merchant = descricao;
    result.transferType = "external";
    return result;
  }

  // Transfer
  if (tipo === "Transferência") {
    result.transferType = "internal";
    // Detect if it's to/from savings
    if (produto === "Poupanças" || descricao.includes("subconta") || descricao.includes("Subconta")) {
      result.linkedAccountType = "savings";
      result.category = "Finances // Transfers other";
    }
    return result;
  }

  // Refund
  if (tipo === "Reembolso" || tipo === "Devolução do cartão de débito") {
    result.category = "Refunds";
    result.merchant = descricao;
    return result;
  }

  // REVX_TRANSFER (crypto/digital assets)
  if (tipo === "REVX_TRANSFER") {
    result.transferType = "internal";
    result.category = "Finances // Transfers other";
    return result;
  }

  // TEMP_BLOCK
  if (tipo === "TEMP_BLOCK") {
    result.category = "Finances // Transfers other";
    return result;
  }

  return result;
}

export function parseRevolutBankingCSV(
  csvContent: string,
  accountIds: { checking: string; savings: string }
): NewBankingTransaction[] {
  const records: RawRevolutBankingRow[] = parse(csvContent, {
    columns: true,
    skip_empty_lines: true,
    trim: true,
    bom: true,
  });

  const results: NewBankingTransaction[] = [];

  for (const row of records) {
    // Skip investment and deposit products
    if (SKIP_PRODUCTS.has(row.Produto)) continue;

    const product = mapProduct(row.Produto);
    const accountId = product === "savings" ? accountIds.savings : accountIds.checking;

    const amount = parseFloat(row.Montante) || 0;
    const commission = parseFloat(row["Comissão"] || row.Comissão || "0") || 0;
    const balance = row.Saldo ? parseFloat(row.Saldo) : null;
    const status = mapStatus(row.Estado);

    const startDate = new Date(row["Data de início"]);
    const completedDateStr = row["Data de Conclusão"];
    const completedDate = completedDateStr ? new Date(completedDateStr) : null;

    const { category, merchant, transferType, linkedAccountType } = categorize(
      row.Tipo,
      row["Descrição"] || row.Descrição || "",
      row.Produto
    );

    results.push({
      accountId,
      date: startDate,
      completedDate,
      description: row["Descrição"] || row.Descrição || "",
      amount: amount.toFixed(4),
      commission: commission.toFixed(4),
      currency: row.Moeda,
      balance: balance !== null ? balance.toFixed(4) : null,
      status,
      category,
      merchant,
      transferType,
      linkedAccountType,
      originalType: row.Tipo,
      originalProduct: row.Produto,
    });
  }

  return results;
}

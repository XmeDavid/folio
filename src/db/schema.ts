import {
  pgTable,
  uuid,
  text,
  timestamp,
  numeric,
  date,
  serial,
  boolean,
  index,
  uniqueIndex,
} from "drizzle-orm/pg-core";
import { sql } from "drizzle-orm";

export const accounts = pgTable("accounts", {
  id: uuid("id").primaryKey().defaultRandom(),
  name: text("name").notNull(),
  broker: text("broker").notNull(),
  type: text("type").notNull().default("investment"),
  baseCurrency: text("base_currency").notNull().default("USD"),
  createdAt: timestamp("created_at").defaultNow().notNull(),
});

export const transactions = pgTable(
  "transactions",
  {
    id: uuid("id").primaryKey().defaultRandom(),
    accountId: uuid("account_id")
      .references(() => accounts.id)
      .notNull(),
    date: timestamp("date").notNull(),
    ticker: text("ticker"),
    type: text("type").notNull(),
    quantity: numeric("quantity", { precision: 18, scale: 8 }),
    unitPrice: numeric("unit_price", { precision: 18, scale: 8 }),
    totalAmount: numeric("total_amount", { precision: 18, scale: 4 }).notNull(),
    currency: text("currency").notNull().default("USD"),
    commission: numeric("commission", { precision: 18, scale: 4 }).default("0"),
    fxRateOriginal: numeric("fx_rate_original", { precision: 12, scale: 6 }),
    createdAt: timestamp("created_at").defaultNow().notNull(),
  },
  (table) => [
    index("idx_transactions_account").on(table.accountId),
    index("idx_transactions_ticker").on(table.ticker),
    index("idx_transactions_date").on(table.date),
    index("idx_transactions_type").on(table.type),
  ]
);

export const fxRates = pgTable(
  "fx_rates",
  {
    id: serial("id").primaryKey(),
    date: date("date").notNull(),
    base: text("base").notNull(),
    target: text("target").notNull(),
    rate: numeric("rate", { precision: 12, scale: 6 }).notNull(),
  },
  (table) => [
    uniqueIndex("idx_fx_rates_unique").on(table.date, table.base, table.target),
  ]
);

export const stockPrices = pgTable(
  "stock_prices",
  {
    id: serial("id").primaryKey(),
    ticker: text("ticker").notNull(),
    date: date("date").notNull(),
    closePrice: numeric("close_price", { precision: 18, scale: 4 }).notNull(),
    currency: text("currency").notNull().default("USD"),
  },
  (table) => [
    uniqueIndex("idx_stock_prices_unique").on(table.ticker, table.date),
    index("idx_stock_prices_ticker").on(table.ticker),
  ]
);

export const bankingTransactions = pgTable(
  "banking_transactions",
  {
    id: uuid("id").primaryKey().defaultRandom(),
    accountId: uuid("account_id")
      .references(() => accounts.id)
      .notNull(),
    date: timestamp("date").notNull(),
    completedDate: timestamp("completed_date"),
    description: text("description").notNull(),
    amount: numeric("amount", { precision: 18, scale: 4 }).notNull(),
    commission: numeric("commission", { precision: 18, scale: 4 }).default("0"),
    currency: text("currency").notNull(),
    balance: numeric("balance", { precision: 18, scale: 4 }),
    status: text("status").notNull().default("completed"),
    category: text("category"),
    categoryManual: boolean("category_manual").notNull().default(false),
    merchant: text("merchant"),
    transferType: text("transfer_type"),
    linkedAccountType: text("linked_account_type"),
    originalType: text("original_type"),
    originalProduct: text("original_product"),
    tags: text("tags")
      .array()
      .notNull()
      .default(sql`'{}'::text[]`),
    createdAt: timestamp("created_at").defaultNow().notNull(),
  },
  (table) => [
    index("idx_banking_tx_account").on(table.accountId),
    index("idx_banking_tx_date").on(table.date),
    index("idx_banking_tx_category").on(table.category),
    index("idx_banking_tx_merchant").on(table.merchant),
    index("idx_banking_tx_tags").using("gin", table.tags),
  ]
);

export const categories = pgTable("categories", {
  id: serial("id").primaryKey(),
  name: text("name").notNull().unique(),
  parentGroup: text("parent_group"),
  subGroup: text("sub_group"),
});

export const merchantOverrides = pgTable("merchant_overrides", {
  id: serial("id").primaryKey(),
  merchantName: text("merchant_name").notNull().unique(),
  category: text("category"),
  createdAt: timestamp("created_at").defaultNow().notNull(),
});

export type Account = typeof accounts.$inferSelect;
export type NewAccount = typeof accounts.$inferInsert;
export type Transaction = typeof transactions.$inferSelect;
export type NewTransaction = typeof transactions.$inferInsert;
export type BankingTransaction = typeof bankingTransactions.$inferSelect;
export type NewBankingTransaction = typeof bankingTransactions.$inferInsert;
export type Category = typeof categories.$inferSelect;
export type MerchantOverride = typeof merchantOverrides.$inferSelect;
export type FxRate = typeof fxRates.$inferSelect;
export type StockPrice = typeof stockPrices.$inferSelect;

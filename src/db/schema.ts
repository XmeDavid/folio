import {
  pgTable,
  uuid,
  text,
  timestamp,
  numeric,
  date,
  serial,
  index,
  uniqueIndex,
} from "drizzle-orm/pg-core";

export const accounts = pgTable("accounts", {
  id: uuid("id").primaryKey().defaultRandom(),
  name: text("name").notNull(),
  broker: text("broker").notNull(),
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

export type Account = typeof accounts.$inferSelect;
export type NewAccount = typeof accounts.$inferInsert;
export type Transaction = typeof transactions.$inferSelect;
export type NewTransaction = typeof transactions.$inferInsert;
export type FxRate = typeof fxRates.$inferSelect;
export type StockPrice = typeof stockPrices.$inferSelect;

CREATE TABLE "accounts" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"name" text NOT NULL,
	"broker" text NOT NULL,
	"type" text DEFAULT 'investment' NOT NULL,
	"base_currency" text DEFAULT 'USD' NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "banking_transactions" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"account_id" uuid NOT NULL,
	"date" timestamp NOT NULL,
	"completed_date" timestamp,
	"description" text NOT NULL,
	"amount" numeric(18, 4) NOT NULL,
	"commission" numeric(18, 4) DEFAULT '0',
	"currency" text NOT NULL,
	"balance" numeric(18, 4),
	"status" text DEFAULT 'completed' NOT NULL,
	"category" text,
	"category_manual" boolean DEFAULT false NOT NULL,
	"merchant" text,
	"transfer_type" text,
	"linked_account_type" text,
	"original_type" text,
	"original_product" text,
	"tags" text[] DEFAULT '{}'::text[] NOT NULL,
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
CREATE TABLE "categories" (
	"id" serial PRIMARY KEY NOT NULL,
	"name" text NOT NULL,
	"parent_group" text,
	"sub_group" text,
	CONSTRAINT "categories_name_unique" UNIQUE("name")
);
--> statement-breakpoint
CREATE TABLE "fx_rates" (
	"id" serial PRIMARY KEY NOT NULL,
	"date" date NOT NULL,
	"base" text NOT NULL,
	"target" text NOT NULL,
	"rate" numeric(12, 6) NOT NULL
);
--> statement-breakpoint
CREATE TABLE "merchant_overrides" (
	"id" serial PRIMARY KEY NOT NULL,
	"merchant_name" text NOT NULL,
	"category" text,
	"created_at" timestamp DEFAULT now() NOT NULL,
	CONSTRAINT "merchant_overrides_merchant_name_unique" UNIQUE("merchant_name")
);
--> statement-breakpoint
CREATE TABLE "stock_prices" (
	"id" serial PRIMARY KEY NOT NULL,
	"ticker" text NOT NULL,
	"date" date NOT NULL,
	"close_price" numeric(18, 4) NOT NULL,
	"currency" text DEFAULT 'USD' NOT NULL
);
--> statement-breakpoint
CREATE TABLE "transactions" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"account_id" uuid NOT NULL,
	"date" timestamp NOT NULL,
	"ticker" text,
	"type" text NOT NULL,
	"quantity" numeric(18, 8),
	"unit_price" numeric(18, 8),
	"total_amount" numeric(18, 4) NOT NULL,
	"currency" text DEFAULT 'USD' NOT NULL,
	"commission" numeric(18, 4) DEFAULT '0',
	"fx_rate_original" numeric(12, 6),
	"created_at" timestamp DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "banking_transactions" ADD CONSTRAINT "banking_transactions_account_id_accounts_id_fk" FOREIGN KEY ("account_id") REFERENCES "public"."accounts"("id") ON DELETE no action ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "transactions" ADD CONSTRAINT "transactions_account_id_accounts_id_fk" FOREIGN KEY ("account_id") REFERENCES "public"."accounts"("id") ON DELETE no action ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_banking_tx_account" ON "banking_transactions" USING btree ("account_id");--> statement-breakpoint
CREATE INDEX "idx_banking_tx_date" ON "banking_transactions" USING btree ("date");--> statement-breakpoint
CREATE INDEX "idx_banking_tx_category" ON "banking_transactions" USING btree ("category");--> statement-breakpoint
CREATE INDEX "idx_banking_tx_merchant" ON "banking_transactions" USING btree ("merchant");--> statement-breakpoint
CREATE INDEX "idx_banking_tx_tags" ON "banking_transactions" USING gin ("tags");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_fx_rates_unique" ON "fx_rates" USING btree ("date","base","target");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_stock_prices_unique" ON "stock_prices" USING btree ("ticker","date");--> statement-breakpoint
CREATE INDEX "idx_stock_prices_ticker" ON "stock_prices" USING btree ("ticker");--> statement-breakpoint
CREATE INDEX "idx_transactions_account" ON "transactions" USING btree ("account_id");--> statement-breakpoint
CREATE INDEX "idx_transactions_ticker" ON "transactions" USING btree ("ticker");--> statement-breakpoint
CREATE INDEX "idx_transactions_date" ON "transactions" USING btree ("date");--> statement-breakpoint
CREATE INDEX "idx_transactions_type" ON "transactions" USING btree ("type");
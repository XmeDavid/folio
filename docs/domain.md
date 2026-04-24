# Domain model

## Entities

- **User** – one per person, owns everything via `user_id`.
- **Account** – anything with a balance (bank, brokerage, cash pot, credit card, loan). Has a `source` (`manual | gocardless | ibkr_flex | camt053_import | csv_import`) indicating where data comes from.
- **Category** – user-defined hierarchical taxonomy for transactions.
- **Transaction** – one posted ledger entry. Signed amount (negative = outflow). Idempotent via `(account_id, external_id)` unique index.
- **ProviderConnection** – one per external link (e.g. one requisition to Revolut, one IBKR Flex token). Secrets are AES-GCM encrypted.

## Money rules

| Rule | Reason |
|---|---|
| Store as `numeric(19,4)` (fiat) or `numeric(28,8)` (crypto) | No float rounding |
| Go side: `shopspring/decimal.Decimal` | Matches `numeric` semantics |
| Wire format: JSON `string`, never `number` | JS `number` is IEEE-754 → loses precision above ~15 digits |
| Every amount carries a currency | No ambiguity, explicit FX |
| FX stored separately (not baked into amount) | Preserves original + converted both |

## Multi-currency

- Each user has a `base_currency` (default `CHF`) for reporting and net-worth rollups.
- Accounts are single-currency. If Revolut has CHF + EUR + USD, that's three accounts under the same GoCardless connection.
- Each transaction records:
  - `amount` + `currency` — the account's currency (what actually hit the balance)
  - `original_amount` + `original_currency` — pre-FX (nullable; same as above if no FX)
- FX rates: pull daily rates (ECB or Open Exchange Rates) into a `fx_rates` table (planned). Reports join on date.

## Idempotent sync

- Every provider transaction has a stable `external_id` (GoCardless `transactionId`, IBKR Flex `tradeID` or `cashTransactionID`).
- Upsert pattern: `INSERT ... ON CONFLICT (account_id, external_id) DO UPDATE`.
- Manual transactions have `external_id = NULL` — never conflict.
- Balance field on `accounts` is a **cached** value from the provider's latest balance pull; don't derive it by summing transactions (transactions history may be truncated).

## Planning (future module)

Not in the initial migration. Rough shape:

- **Budget** – period (monthly/yearly) + category limits.
- **Goal** – target amount + date (e.g. "6 months emergency fund").
- **Forecast** – projects balances forward using recurring transactions + planned cashflows.
- **Recurring** – pattern-matched from historic transactions (detected by counterparty + cadence).

## Soft invariants worth enforcing

1. A transaction's `currency` must match its account's `currency`. (Check constraint or service-level guard.)
2. Deleting an account cascades to its transactions (already in schema).
3. Archived accounts are excluded from sums by default (query helper).
4. Provider connections in `status = 'error'` block their sync worker until resolved.

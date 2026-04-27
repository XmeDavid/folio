# Folio — Feature Specification

**Status:** Living reference document
**Last updated:** 2026-04-27
**Audience:** Future-you, contributors, planning sessions

This document captures **everything Folio intends to be**. It is domain-organised and **does not prioritise**. Implementation sequencing happens in a separate plan. If a feature is listed here, it is in scope for Folio — the only question is when.

---

## 1. Overview & principles

Folio is a self-hosted personal finance & planning app. **Workspace-scoped:** each workspace is a shared financial container (its own accounts, categories, base currency, cycle anchor) with one or more members. Users are platform-level identities that can belong to and switch between multiple workspaces. Data is isolated per workspace; sharing happens only via explicit invites.

**Core principles:**

- **Bank is the source of truth for what happened**; Folio is the source of truth for what you plan to happen and what things mean.
- **Close the loop between intention and reality.** Folio is not only a tracker and not only a planner: it plans money before it moves, observes what actually happened from transactions and balances, reconciles the two, then improves the next plan.
- **Every amount carries a currency.** Base currency per workspace (chosen on workspace creation, eg. CHF) for reporting rollups; original currency always preserved.
- **No float math.** Decimal everywhere.
- **Users own their data.** Full export, full delete, offline-capable PWA. Export and delete operate at workspace scope (per workspace) and at account scope (a user account, with all the workspaces it solely owns).
- **Accrual on expenses, cash-aware on cashflow planning.** CC swipes count as expense when they post; paying the CC bill is a scheduled transfer, not a new expense.
- **Backfill and historical corrections are first-class.** Users don't start using a finance app on day one of their financial lives. Starting points, checkpoints, and retroactive edits are expected.

### Workspaces & membership

A **workspace** is the financial container. All accounts, transactions, categories, budgets, goals, trips, etc. scope to a single workspace.

- **Workspace fields:** name, slug, base currency, cycle anchor day, locale, timezone.
- **Roles:** `owner` and `member`. Owners manage workspace settings, invite/remove members, and change roles. Members read/write financial data and can self-leave. No per-resource ACLs in v1.
- **Users vs. workspaces:** a user account is platform-level (email, password, MFA, passkeys). One user can belong to many workspaces; one workspace can have many users. `last_workspace_id` drives the default workspace on login; the UI surfaces a workspace switcher.
- **Invites.** An owner invites by email with a target role; the invitee receives a tokenized email link. To accept, the invitee must be authenticated with a verified email matching the invite. Acceptance creates the membership row. Invites expire; the original inviter or any owner can revoke.
- **Cold signup vs. join.** Signing up cold creates a fresh workspace with the new user as `owner`. Joining an existing workspace is always invite-mediated; there is no "search & request to join" flow.
- **Invariants.**
  - **Last owner:** a workspace must always have ≥1 owner. The final owner cannot be removed or demoted.
  - **Last workspace:** a user must always have ≥1 workspace. Leaving your last workspace is blocked; create another first.
- **Platform admin console.** Separate from workspace-level administration. The self-host operator sees a workspace list with member counts, last activity, and deletion state. Workspace owners cannot reach the platform admin surface.
- **Audit log scope.** Audit entries belong to a workspace and capture the acting user, so multi-member edits are attributable.

---

## 2. Accounts

Every balance in Folio lives on an account.

### Account types

- **Checking / Savings / Cash pot** — standard bank-style accounts. Cash pots support arbitrary currencies (Euros in a drawer, USD in a hotel safe).
- **Credit card** — liability account, negative balance. Per-swipe transactions hit expense stats immediately; bill payment surfaces as a scheduled transfer from the paying account.
- **Investment / Brokerage** — holds positions + cash balance.
- **Crypto** — wallets. Manual, or read-only from public addresses.
- **Loan / Personal debt** — liability account.
- **Mortgage** — liability account with optional amortisation schedule (principal/interest split per payment).
- **Asset (physical)** — car, house, watch, art, collectibles. User-maintained valuation, optional plugin-based market lookup (e.g. Eurotax for cars). Rolls into networth; excluded from *liquid* savings-rate math.
- **Pillar 3a** — special read-only account type (Swiss retirement). Manual balance update, typically annual from statement.
- **Pillar 2 (LPP/BVG)** — same pattern as 3a: manual annual balance update.
- **Manual (generic)** — catch-all for anything Folio doesn't model explicitly.

### Account capabilities

- **Opening balance + start date** (mandatory). No stats or networth calculations extend before the start date. Each account can have its own start date (eg. Revolut since 2019, PostFinance since 2024, auto detected also by first transaction or user overwrite).
- **Nickname** — e.g. "Revolut main", "UBS house fund".
- **Archival** — closed accounts hide from sums by default; history preserved up to close date.
- **Per-account currency** — single currency per account. Multi-currency provider (Revolut CHF + EUR + USD) = three accounts under one provider link.
- **Source linkage** — `manual | gocardless | ibkr_flex | camt053_import | csv_import | crypto_address`.
- **Included-in-networth flag** — default on; can be turned off for specific accounts.
- **Included-in-savings-rate flag** — default derived from type (liquid yes, asset no).

### Account groups

Account groups are user-created, workspace-scoped containers for organizing many
accounts into the user's mental buckets without changing the ledger model.

- **Examples** — `Crypto`, `MMF`, `Personal banking`, `Retirement`, `Debt`, `Physical assets`.
- **Editable user data** — users can create, rename, delete, reorder, and archive groups. Folio may suggest groups during imports/onboarding, but they are not hardcoded system categories.
- **Accounts remain the accounting truth** — transactions still post to exactly one account; groups roll accounts up for navigation, filtering, dashboards, and reports.
- **Membership** — an account has one primary group for account-list organization. Later, optional secondary group membership can support cross-cutting views if needed.
- **Ordering** — groups have a user-defined sort order, and accounts have a user-defined order within their primary group.
- **Drag and drop** — the Accounts page supports dragging groups to reorder them, dragging accounts within a group to reorder them, and dragging accounts between groups to move them. Keyboard-accessible move controls must provide the same behavior.
- **Rollups** — group headers show base-currency totals, with native account currencies preserved in the expanded rows.
- **Imports** — import flows should suggest a target group from account kind/source, such as `crypto_wallet` → `Crypto` and MMF/brokerage cash-like accounts → `MMF`, while letting the user override before applying.

---

## 3. Connectivity & imports

- **Direct bank connections** via GoCardless Bank Account Data (~2,500 banks, EEA/UK).
- **Interactive Brokers** via IBKR Flex Web Service (per-user token).
- **Revolut Trading CSV import** for brokerage transactions, including original FX rate capture where the export provides it.
- **Revolut Savings / Money Market import** for mixed banking + investment statements: banking rows become cash transactions; fund buy/sell rows become investment events.
- **CAMT.053 XML import** (ISO-20022) for PostFinance and other banks providing XML statements.
- **Generic CSV import** with user-defined column mapping. Saved profiles per bank/format for reuse.
- **Named import presets** for other finance apps: Mint, YNAB, Actual, Firefly. Preset fills in the CSV mapping.
- **Manual entry** always available on every account.
- **Sync status surface** per connection: last-synced-at, last error, next scheduled sync, reconnect CTA when token/consent expires.
- **Parser library is extensible** — new bank formats ship as new parser profiles without core code changes.
- **Email-to-import** (optional later) — forwarded statements from banks that only email.
- **Public crypto address read-only** — paste address, Folio pulls balances from a blockchain indexer.

---

## 4. Transactions

The core ledger entry. Every posted, planned, or scheduled financial event is a transaction.

### Fields

- **Signed amount + currency** (account currency).
- **Original amount + currency** (pre-FX; nullable if same as amount).
- **Dates** — posted, booked, value date (whichever the source provides).
- **Merchant** — 0 or 1 (first-class entity, see §5).
- **Category** — exactly 1 leaf category.
- **Tags** — many (user-added + auto-applied from source metadata).
- **Status lifecycle** — `planned → scheduled → paid`. Bank-sourced transactions enter as `paid` (or `reconciled`).
- **Notes** — free text.
- **Attachments** — receipts, invoices, screenshots, arbitrary files (see §23).
- **Count-as-expense flag** — default derived from category + amount sign. Override available (e.g. asset purchases that are both spend and balance-sheet addition).
- **Edit history** — every edit logged (read-only audit trail).
- **Split sublines** — one transaction → N sublines, each with own amount/category/merchant/tag/note. Sublines sum to parent amount.
- **Linked-to references** — transfer partner, refund pair partner, split-bill receivable, recurring template, goal contribution, trip.

### Transaction lifecycle

- **Planned** — user-created as part of planning; no real-world payment yet.
- **Scheduled** — planned transaction with a specific future date and intent to execute.
- **Paid** — executed (marked manually, or matched to a synced bank transaction).
- **Reconciled** — paid AND verified to match a bank record (auto on sync match, or manual).

### Auto-matching (dedupe)

When a bank sync brings in a transaction, Folio tries to match it to an existing `planned`/`scheduled`/manual `paid` transaction: amount within tolerance, date within window, merchant similarity. On match, the entries merge (bank data authoritative, user fields preserved). Prevents double-counting when the user has entered a transaction manually (or via receipt capture) before the bank sync.

---

## 5. Categorisation system

### Categories

- **Hierarchical**, user-editable. Example: `Food → Groceries`, `Food → Restaurants`, `Food → Delivery`.
- Each transaction → **exactly one leaf category**.
- Budgets and stats roll up the tree (budget `Food 800` can break down into leaves).
- User can rename, move, merge, delete categories. History preserved.
- Seed defaults on onboarding, fully customisable.

### Merchants (first-class)

- Each transaction has 0 or 1 merchant.
- Merchant carries: canonical name, logo (optional), default category, alternate raw strings (e.g. `COOP-4382 ZUR` → `Coop`), metadata (industry, website).
- **Drill-down**: "spend at Migros, year-to-date"; "transactions at Amazon, all time".
- User can merge merchants ("Apple Inc." + "APPLE.COM/BILL" → Apple).

### Merchant enrichment

- **Local rules (regex)** — raw string → cleaned merchant + default category.
- **External provider** (later) — optional paid integration for rich enrichment.

### Tags

- Flat, many-to-many, orthogonal to categories.
- Auto-applied from import metadata (MCC codes, GoCardless category hints, CSV columns).
- User can add/remove tags manually.
- Used for **filtering and cross-cutting slices**, not rollups: `#vacation-paris`, `#reimbursable`, `#tax-2026`, `#work`, `#coffee-tracker`.

### Uncategorised

- Its own bucket with a dedicated UI flow to clear it out.
- Optional hard-fail: prevent a transaction from affecting budgets until categorised (user preference).

### Rules engine

- User-defined rules: `if (merchant contains X AND amount > Y AND account = Z) → set category=A, add tag=B`.
- Run at sync time.
- Rule order matters (first match wins; explicit override).
- Manual per-transaction override always beats rules.

### AI categorisation

- Opt-in, paid tier.
- **Suggestion-only** (never auto-applies without confidence threshold user sets).
- Runs on transactions that remain uncategorised after merchant defaults and rules.
- Backends: self-hosted (Ollama) or user BYO API key (OpenAI/Anthropic).

### Split transactions

- One transaction → N sublines.
- Each subline: amount, category, merchant (inherits parent by default), tag, note.
- Sublines must sum to parent amount (validation).
- Useful for Costco-style mixed purchases, shared dinners before settlement, mixed-category receipts.

---

## 6. Transfers & matching

Transfers between the user's own accounts are **not a separate data type**; they are a **matched-pair layer** over real transactions.

- **Auto-detect pairs** on sync: absolute amount within tolerance (accounts for fees and FX), compatible account pair, dates within window.
- **Manual pairing UI** for matches the detector misses; supports cross-currency pairing (FX rate captured at the time of the transfer).
- **Paired transactions never count in income/expense stats**, regardless of whether the user has them visible.
- **Visibility toggle**: hidden from transaction lists by default; user can reveal.
- **Unmatched transfer state**: one leg in Folio, other leg external (account Folio doesn't know). User marks "outbound to external" — stays excluded from stats.
- **CC bill payments**: same primitive. Transfer from checking → CC account.
- **Mortgage principal payments**: same primitive. Transfer from checking → mortgage (reducing liability).
- **Loan repayments**: same primitive.

---

## 7. Income & expenses

### Income sources

- Multiple sources per user.
- Each source: name, account it lands in, amount type (fixed | variable), cadence (recurring weekly/biweekly/monthly/yearly, or one-off), tax hints (gross/net, optional).
- Examples: primary salary (recurring), freelance gig (variable recurring), birthday gift (one-off), dividend income (variable monthly/quarterly).
- **Expected vs. actual** per cycle for variable sources.

### Expense types

- **One-off** — single occurrence, no recurrence.
- **Flexible (aka budgeted)** — categories you know you spend in every cycle but amount varies. Examples: groceries, restaurants, entertainment. Has a monthly budget, no fixed amount.
- **Recurring** — known recurring payments. Has:
  - **Amount type**: fixed (1,800 CHF) OR **percentage of income for that cycle** (e.g. 10% tithe).
  - **Start date** + **optional end date** (for contracts with a term).
  - **Cadence**: monthly, quarterly, yearly, custom interval.
  - **Per-cycle override**: for any given cycle, user can overwrite actual paid amount.
  - **Share %** (optional): Netflix split 4 ways → only 25% hits your budget.
  - **Cancel URL** (optional) — subscription hygiene.
  - **Notes, attachments** (contract PDFs, etc.).

### Subscriptions view

- Derived lens over recurring expenses tagged as subscriptions.
- Shows: next renewal dates, monthly-equivalent cost, annual total, cancel URLs, contract end dates.
- Price-change detection (alert when a recurring charge changes amount).

---

## 8. Payment cycles & planning

Planning is Folio's forward-looking layer over the ledger. The core promise:
turn a salary/payment cycle into a practical deployment plan, then use real
transactions and balances to prove whether the plan happened.

Most budgeting tools stop at "you planned 600 for groceries". Folio should
answer:

- What does this paycheck need to cover before the next one arrives?
- Where should the money go, account by account and currency by currency?
- Which transfers/payments/conversions must happen?
- Did the income arrive as expected?
- Did the payments, transfers, and investments actually happen?
- Is the user still on track, and what should change now?
- What did this cycle teach us about the next one?

### Payment cycles

- Workspace-defined cycle anchor (typically the primary earner's salary date, e.g. "25th of each month"). Lives on the workspace, so all members share the same cycle.
- Cycle = one planning unit. Most workspaces = monthly; biweekly / custom supported.
- Stats and budgets default to cycle-aligned windows; user can switch to calendar-month views.
- Cycle windows are payday-to-payday, not necessarily calendar months. A cycle can be 28, 30, 31, or custom days, and daily budget pace adapts to the actual length.
- Folio knows which obligations fall before the next payday and surfaces timing risk when a heavy bill cluster happens early in the cycle.

### Planning a cycle

For each upcoming cycle, user sees / edits:

- **Expected income** (pre-filled from income sources, editable).
- **Recurring expenses** (pre-filled from active recurring templates, per-cycle override supported).
- **Flexible budgets** (pre-filled from previous cycle, editable).
- **Planned one-off expenses** (user adds).
- **Savings rules** (see §9 — pre-filled from previous cycle, editable).
- **Travel budgets** (pulled from active trips falling in cycle — see §14).
- **Planned investments** (recurring DCA buys, etc.).

The user should not need to duplicate data. A plan is assembled from existing
Folio facts:

- Income sources and detected paydays.
- Recurring templates and subscriptions.
- Category budgets and rollover policies.
- One-off transactions, wishlist items, trips, reimbursements, tax events, asset maintenance, and other dated commitments.
- Existing account balances, account groups, goal buckets, investment accounts, liabilities, and available cash.
- Historical transaction patterns that suggest recurring costs, unusual one-offs, or unrealistic budgets.

### Pre-fill

- If previous cycle exists → pre-fill every slot with that cycle's values.
- First-ever cycle → pre-fill recurring from templates, leave flexible/one-off blank, prompt user.
- Later cycles should pre-fill from a blend of user intent and history: last plan, previous actuals, seasonal patterns, known annual/quarterly bills, and confirmed recurring discoveries.
- Folio can propose changes before the cycle starts: "Groceries has averaged 680 for three cycles; planned 500 may be unrealistic."
- Suggested lines are always reviewable. The user remains in control of the plan.

### Allocation plan

The allocation plan describes where money should live after income lands.

- Supports fixed amounts: "move 500 CHF to Emergency Fund".
- Supports percentage rules: "invest 15% of actual net income".
- Supports priority order: rent and bills first, sinking funds next, savings/investments after, flexible spending last.
- Supports percentage-of-remainder rules: "after commitments and buffers, send 70% of remainder to brokerage and 30% to travel".
- Supports account targets: checking buffer, credit-card payoff, savings pot, brokerage contribution, mortgage overpayment, virtual goal bucket.
- Supports multiple currencies and detects when an allocation needs conversion before it is executable.
- Can be saved as the default for future cycles, with per-cycle overrides.

### Action plan

- Derived view: given the plan, what payments / transfers does the user need to execute?
- Per-instruction: "transfer 2,000 from Salary Checking → UBS Savings on May 26", "pay rent 1,800 on May 28".
- Instructions include ordinary payments, internal transfers, credit-card payoff, goal contributions, investment contributions, currency conversions, reimbursement follow-ups, and liquidity/buffer actions.
- Instructions have status: `pending → scheduled → done → skipped/dismissed`.
- Marking done can create/link a scheduled/paid transaction, but bank-synced transactions can also auto-complete the instruction.
- Folio watches the ledger for matching transactions: "500 moved from Checking to Savings" closes the planned transfer automatically.
- Missing execution is visible: "Rent expected by Apr 30; no matching payment detected."
- Partial execution is visible: "Planned 900 to investments; 600 detected, 300 remaining."
- The action plan should be exportable/shareable as a compact checklist for the current cycle.

### Currency conversions

- Every plan line keeps its native currency and reporting/base-currency value.
- The plan highlights cross-currency obligations before they become surprises.
- Example: salary lands in CHF, rent is EUR, investment buy is USD, trip budget is GBP.
- Folio computes the estimated conversion need using current/historical FX and later reconciles against the actual conversion transaction/rate.
- Conversion actions can be explicit: "Convert 850 CHF → EUR before rent on Apr 28."
- FX variance is tracked separately from spending variance, so the user can tell whether the plan drift came from behavior or rates.

### Planned vs. actual

- Once the cycle is in progress, user sees plan vs. reality per line:
  - Recurring expenses: planned 1,800 / paid 1,800 ✓
  - Flexible (Groceries): planned 400 / spent-so-far 312 / projected 387
  - Income: expected 8,000 / received 8,042 ✓
- End-of-cycle summary: overall variance, category breakdowns, rollover calculations (see below).
- Actual income replaces expected income when it lands. Percentage allocations and remainder rules recompute from the actual number.
- If income is lower than planned, Folio proposes safe adjustments in priority order instead of silently leaving the plan impossible.
- If income is higher than planned, Folio proposes where the surplus should go according to rules: goals, investments, debt, buffer, or discretionary.
- Category budgets show spent, remaining, daily pace, projected end-of-cycle spend, and transactions causing drift.
- Recurring expenses show expected, paid, unpaid, amount changed, paid early/late, and likely duplicate/missed payment.
- One-off expenses can be planned ahead or detected from unusual actual transactions during/after the cycle.
- Transfer and investment allocations are tracked against real account movements, not just marked done manually.
- The plan is a living forecast: every sync can update remainder, runway, savings rate, investment rate, and cash buffer risk.

### Automatic discovery

Folio should use transaction history to reduce manual setup:

- Detect recurring income and paydays.
- Detect recurring expenses, subscriptions, quarterly/yearly bills, and amount changes.
- Detect one-off expenses that should be named and explained rather than hidden inside ordinary category spend.
- Detect planned transfers that did not happen, extra transfers that happened without a plan, and mismatched amounts.
- Detect category budgets that are consistently too low/high.
- Detect upcoming obligations from historical cadence even before the user creates a template.
- Every detection is a suggestion first; user confirmation turns it into durable planning data.

### Retrospective loop

At cycle close, Folio summarizes what the plan got right and wrong:

- Planned inflow vs. actual inflow.
- Planned commitments vs. actual paid commitments.
- Flexible budget variance by category, merchant, and transaction.
- Planned transfers/allocations vs. detected account movements.
- Planned investments vs. actual contributions/trades.
- Currency conversion estimate vs. actual conversion rate/amount.
- Savings rate, investment rate, runway, and net worth change for the cycle.
- Which assumptions should change next cycle.

The retrospective feeds the next plan. Folio should learn user-specific truth:
"restaurants is usually 20% over", "salary sometimes lands one day early",
"December has higher gifts/travel", "insurance hits quarterly", "this sinking
fund needs a larger monthly contribution".

### Rollover (per-category)

- User chooses per-category behaviour:
  - **Reset**: next cycle starts at budget amount, leftover just stays as uncommitted cash (handled by savings rules if any).
  - **Rollover**: next cycle's budget starts at `this_cycle_budget + leftover`. Classic envelope behaviour.
  - **Rollover with cap**: rollover up to a cap, then reset.
- Overspend handling symmetric: eat into next cycle (negative start) or zero out.
- Default: reset. User opts in per category.

---

## 9. Goals & savings

### Goals (target-based)

- **Fields**: name, target amount, current amount, optional deadline, linked account(s) or virtual sub-balance, category tag (retirement, travel, emergency, house, sabbatical…).
- **Progress tracking**: % complete, ahead/behind schedule.
- **Forecast**: at current contribution rate, ETA to target.
- **Priority order**: user ranks goals; savings rules can respect this.

### Sinking funds (annuity smoothing)

- Sub-type of goal: recurring target (e.g. car insurance 1,200 CHF every November → save 100/month).
- Auto-create sinking fund from a yearly recurring expense.
- Pause / resume / adjust.

### Virtual sub-balances

- A single savings account at the bank can hold multiple earmarked buckets inside Folio.
- Example: "UBS Savings — 12,200 CHF" = 1,200 (car insurance) + 3,000 (emergency fund slice) + 8,000 (house deposit).
- Sub-balances are Folio-side labels only; no actual sub-accounts at the bank.
- Transfers into/out of a goal/sinking fund tag the transaction with the bucket.

### Savings rules engine

- User-defined rules for how leftover / income is distributed.
- Rule primitives:
  - "X% of leftover at end of cycle → goal Y"
  - "Fixed X CHF from income → sinking fund Y"
  - "Top up goal Y to maintain balance Z"
  - "After goal Y hits target, redirect its contributions to goal Z"
- Rules evaluate in user-defined order.
- Output: contribution suggestions surfaced in the action plan.

---

## 10. Reconciliation & checkpoints

### Default trust-the-bank

- On every sync, `account.balance = latest_bank_balance`.
- Any local-only transactions that don't reconcile surface as "drift — investigate".
- User can manually override any balance if they know the bank is wrong (rare).

### Manual reconciliation workflow

- User triggers "close the books" for an account up to a date.
- Folio shows all transactions in the window, user ticks each as matching their statement.
- Mismatches surface: missing transactions, duplicates, incorrect amounts.
- On completion, a **checkpoint** is recorded.

### Checkpoints

- A checkpoint is a user assertion: "on date D, this account had balance B".
- Supported scenarios:
  - **Onboarding**: user sets the opening balance when the account is created; that's the initial checkpoint.
  - **Backfill**: user adds historical transactions (e.g. from old CSV dumps) after creating the account. Additional checkpoints can be set at any prior date.
  - **Periodic peace-of-mind**: user runs reconciliation monthly; each successful close writes a checkpoint.
- Folio flags drift between checkpoints and the computed balance from transactions.
- **Backfill triggers snapshot recomputation** — networth history and stats for affected periods re-derive automatically.

---

## 11. Investments

### Positions

- Ticker / symbol, quantity, average cost, current price, market value, unrealised P/L.
- Per-account breakdown (same ticker across IBKR + Revolut = two lot sets, or user-merged).
- Cost basis method: FIFO default; specific-lot available for tax-sensitive cases.
- Position history is replayable from underlying events; current positions are a cache, not the source of truth.

### Trades

- Buy / sell history: date, price, quantity, fees, account.
- Derived: realised gains/losses per trade, holding period.
- Every investment event is captured: buys, sells, transfers in/out, deposits/withdrawals, fees, taxes, dividend reinvestments, rewards/airdrops for crypto-like assets, and manual adjustments.
- Trades can link to cash ledger transactions without making the cash movement count as ordinary income/expense.
- Broker import taxonomies are preserved and mapped into canonical investment events: market/limit buys and sells, stock splits, mergers paid in stock, position closures, custody/robo fees, dividend tax corrections, and broker-side FX buy/sell rows.
- Account-level investment fees that are not tied to a ticker still count against portfolio return.

### Dividends

- Dividend events per position: pay date, amount, currency, tax withheld (optional).
- Yield-on-cost calculation.
- Gross dividends, withholding tax, net received, and reinvested dividends are all separately reportable.
- Dividend income can appear in income views when the user wants that lens, but investment dashboards also show it as portfolio return.

### Valuation history & performance

- Daily position valuation history: quantity held, close price, market value, cost basis, unrealised P/L, realised P/L, dividends, fees, and total return.
- Portfolio performance views separate **contributions/withdrawals**, **market movement**, **realised gains**, **unrealised gains**, **dividends**, **fees/taxes**, and **FX impact**.
- Supports money-weighted and time-weighted return where enough history exists; simple P/L remains the default view.
- Investment dashboard: total value, day/month/YTD/all-time P/L, realised vs unrealised, dividend income, allocation, top movers, exposure by currency/asset class/bucket/account.
- Charts can switch between **total return** (price movement + realised P/L + dividends - fees/taxes) and **price-only return** (price movement + realised P/L).
- Drill-down from dashboard → account → position → lot/trade/dividend history. Individual stock view shows holdings over time, trade markers, dividends, cost basis, and price/value chart.

### Multi-currency investment views

- Instrument currency is preserved (e.g. AAPL in USD), account currency is preserved, and reporting currency is user-selectable per view.
- Any investment chart can be displayed in the workspace base currency or another chosen currency (e.g. USD-priced stock shown in CHF).
- Historical valuations use the FX rate for that valuation date, not today's FX rate.
- FX impact is shown separately where useful: "AAPL rose in USD, but USD/CHF movement reduced CHF return."
- Missing FX rates use the latest available prior business-day rate, with gaps surfaced if the date is outside provider coverage.

### Asset-class tagging

- Each position tagged with one or more buckets: **ETF core**, **mega-cap quality**, **dividend**, **thematic**, **speculative**, **crypto**.
- Target allocation: user defines desired % per bucket.
- Current allocation vs. target → drift view.

### Corporate actions

- Splits, reverse splits, mergers, delistings, spin-offs, symbol changes.
- Manual entry UI: "PARAA delisted on 2025-08-06, became PSKY at ratio X" or "received $23 cash per share".
- System re-bases cost and quantity correctly.
- Symbol aliases and ticker lifecycle cleanup are first-class: broker exports may refer to the same instrument under old/new symbols, exchange suffixes, ISINs, or delisted tickers.
- Manual closure policy for dead/delisted positions: user can close remaining quantity at zero or with cash received, with an auditable event.

### Price source

- Broker-reported prices where available (IBKR sync provides latest).
- External provider for manually-tracked positions (Revolut Shares, crypto) — one primary, one fallback.
- Historical instrument prices are stored server-side as global reference data, shared across workspaces.
- Refresh policy: daily end-of-day backfill for held/watchlisted instruments; intraday/latest quotes are optional and never required for historical reports.
- Current/latest quotes are short-lived cache data; historical closes are durable shared reference data.
- Provider data is cached; Folio does not call market-data or FX providers on every chart render.
- Market-data views expose stale/error states and a manual refresh/bypass-cache action.
- Manual price overrides are available for private/illiquid assets and for correcting provider gaps.

### FX source

- FX rates are global reference data, shared across workspaces and stored separately from transactions/trades.
- Preferred free baseline: ECB daily reference rates for supported currencies; compute non-EUR pairs (e.g. USD→CHF) via EUR when needed.
- Optional fallback/wider-coverage provider for currencies ECB does not cover.
- Daily refresh after provider publication time, plus on-demand historical backfill when importing older trades.
- Provider rows are immutable reference observations; corrected/replaced rates are inserted with source metadata rather than rewriting user transactions.

### Investment reports

- Total portfolio value, cost basis, unrealised P/L (by position, by bucket, overall).
- Realised gains/losses by tax year (export-ready).
- Bucket composition pie / weights.
- Allocation vs. target over time.
- Dividend report: gross/net/withheld by year, account, instrument, and currency.
- FX exposure report: holdings and returns by currency, including base-currency drag/boost.
- Performance attribution: return by position, bucket, account, currency, and time period.
- Exportable transaction/lot/dividend/valuation history for audit and tax work.

### Other ideas

- AI Duplicate-exposure warnings (VUAA + VUSA, VT + regional overlap).
- Watchlist with price alerts.
- Crypto wallet scraping via public addresses.


---

## 12. Physical assets

Assets are accounts of type `asset` with user-maintained valuation.

- **Purchase flow**: user records a regular expense from the paying account (cash leaves). Separately, creates an asset account with opening balance = purchase price. Purchase transaction can be linked to the asset account for traceability.
- **Valuation updates**: periodic manual entry, or optional market-value plugin (e.g. Eurotax for cars; Zillow-equivalent for houses; manual for art/watches).
- **Networth**: assets roll in.
- **Savings-rate math**: assets excluded (they're not liquid).
- **Sale**: inverse flow — asset account closes, income transaction hits the receiving account.
- **Depreciation**: optional user-configured schedule (straight-line, declining balance, or manual).
- **Notes and History**: Attach transactions to the asset(eg. oil change of a car), documents, attachements, and any other notes.
---

## 13. Swiss tax & retirement
On if user is in Swizerland
- **Pillar 3a** account type: read-only asset account, user updates balance annually from statement. Contribution tracking (max legal contribution reached? remaining?).
- **Pillar 2 (LPP/BVG)** account type: same pattern — read-only asset, annual update.
- **Wealth tax summary**: year-end snapshot of net wealth (all accounts, all currencies, converted to CHF) — export for tax declaration.
- **Capital gains tracking**: per-lot for investments, realised gains/losses log per tax year, export-ready.
- **Mortgage interest tracking**: per-payment split into interest (deductible) vs. principal, annual summary.
- **Tax-year filter**: everywhere stats offer date range, "tax year 2026" is a preset.

**Out of scope**: direct e-filing / integration with Swiss tax software. Folio provides clean export-ready data, never a filing tool.

---

## 14. Travel

Travel is a full trip-planning workspace inside Folio, not only a tag on
transactions. A trip combines itinerary, budget, booking records, documents,
participants, split bills, reimbursements, FX, and actual transaction
reconciliation.

### Trip entity

- **Fields**: name, destination(s), start date, end date, base/trip currency, participants (solo / group), overall budget, cover color/icon/banner, notes.
- **Trip status**: planning -> confirmed -> active -> completed -> cancelled.
- **Financial summary**: budget, total planned/booked/paid/pending, remaining budget, paid percentage, per-currency totals, FX estimate vs. actual.
- **Per-category budgets** within a trip: flights, accommodation, food, activities, local transport, vehicle rental, shopping, insurance, documents/visas, other.
- **Collaborators**: workspace members can be invited onto a trip with role/access. Non-workspace people can exist as trip participants for split bills without receiving app access.
- **Shared timeline**: all dated items appear in an itinerary grouped by day; undated costs stay in a costs/backlog section until scheduled.
- **Drag and organize**: reorder itinerary items, move items between days, and move generic costs into the itinerary when dates become known.

### Trip items

Every trip item is both a planning object and a potential financial object.

- **Common fields**: name, item type, status, amount, currency, estimated/actual flag, date or date range, location, provider/vendor, booking/reference number, notes, attachments, linked transaction(s), split settings.
- **Status lifecycle**: planned -> booked -> paid -> cancelled. Paid can be manual or detected from linked transactions.
- **Amount lifecycle**: estimated amount, booked amount, paid amount, refunded amount, user share, reimbursable/share amount.
- **Native currency preserved**: a CHF flight inside a EUR trip stays CHF, with EUR conversion shown for budget rollups.
- **Item-level FX**: Folio stores the rate source/date used for estimates and later compares it with the actual card/bank FX rate.
- **Move to/from costs**: an item can start as a generic cost, become a dated itinerary item, or be moved back if timing is unknown.
- **Multiple transactions per item**: deposits, partial payments, final balance, refunds, damage deposits, card holds, and reimbursements can all attach to the same trip item.

### Typed item forms

The item form changes based on the travel item type so the user can fully plan
and monitor the trip without scattering details across notes.

- **Flight**: airline, flight number, departure airport, arrival airport, departure/arrival time, terminal/gate where known, seat, baggage, booking ref, ticket number, check-in URL, boarding pass attachment.
- **Hotel / lodging**: property name, address, check-in/out dates and times, room/guest details, provider, booking ref, cancellation deadline, deposit/prepaid/balance due, voucher attachment.
- **Vehicle rental**: pickup/dropoff location and time, rental company, car class/model, driver, booking ref, deposit/hold amount, insurance/excess notes, fuel policy, rental agreement attachment.
- **Train / bus / ferry / local transport**: operator, route/from/to, departure/arrival time, ticket/pass details, platform/seat where known.
- **Activity / event / reservation**: venue, start/end time, ticket/reservation number, participant count, cancellation deadline, ticket/voucher attachment.
- **Food / restaurant**: reservation time, restaurant/location, party size, deposit/minimum spend, split settings.
- **Insurance / visa / documents**: policy/application/reference numbers, coverage dates, relevant people, required documents, expiry/reminder dates.
- **Generic cost**: fallback for anything that does not need a specialized schema, still with amount, status, date, notes, attachments, and split support.

### Attachments and documents

- Attachments exist at both trip level and item level.
- Trip-level files: passports, insurance, global itinerary PDF, emergency contacts, general travel documents.
- Item-level files: flight tickets, boarding passes, hotel vouchers, rental contracts, receipts, booking confirmations, activity tickets.
- Supported files: PDF and images at minimum; later, email import and OCR can extract booking details and costs.
- Documents can be linked to a participant when relevant, e.g. one person's visa or ticket.
- Attachment storage shows quota usage and supports bulk download/export with the trip.

### Ledger integration

- Any transaction can be linked to a trip and optionally to a specific trip item.
- Folio can suggest matches from merchant, amount, currency, date, location, booking reference, and imported receipt metadata.
- When a matching bank transaction arrives, the item becomes paid/reconciled automatically.
- Card holds and deposits can be marked as non-expense until captured; refunds net against the original item.
- Trip spending can be included in normal category stats, shown as a trip slice, or isolated in trip reports depending on the lens.
- Planned trip costs feed into the salary-cycle plan as dated commitments before the trip and during the trip.
- Remaining trip cash need is visible before departure: unpaid bookings, planned local spending, pending conversions, and expected reimbursements.

### Shared trip expenses (split-bills integrated)

- Per-trip participant list: workspace users, invited collaborators, and manual people.
- Any trip item or transaction can be split across participants: equal, by parts/shares, by percentage, by fixed amounts, or by itemised lines.
- A trip item can remain personal or become shared later.
- Per-trip ledger: who paid what, who owes whom, running balances, pending reimbursements, settled amounts.
- Settlement: mark a payment as settling the balance with person X; closes out the receivable.
- Reimbursements link back to the original trip item so shared costs do not distort income/expense stats.
- Multi-currency splits are supported: original charge currency, payer account currency, participant settlement currency, and base-currency reporting.
- Final settlement view answers: "who should pay whom now to close the trip?"

### Monitoring and retrospectives

- During planning: show what is missing, unpaid, unbooked, over budget, or lacking documents.
- During travel: show today's itinerary, paid/unpaid items, local currency budget remaining, emergency docs, and pending shared expenses.
- After travel: total spend, planned vs. actual by category/item/person/currency, FX variance, refunds still pending, settlement status, cost per day, cost per participant.
- Trip retrospectives feed future planning: average flight cost to a region, realistic food/local transport spend, recurring travel style, and seasonal FX/budget assumptions.
- Completed trips stay as a searchable archive of itinerary, documents, receipts, actual transactions, and settlement history.

---

## 15. Wishlist

- **Items**: name, estimated price, currency, optional URL, notes, priority.
- **Priority ordering**: user ranks; top-priority items surface in the planning view as candidate one-off expenses.
- **Price tracking (optional)**: paste URL, Folio periodically scrapes price, alerts on drops. Best-effort, non-guaranteed. Post-base feature.
- **Convert to purchase**: marking an item as "bought on date X for amount Y" converts it to a one-off expense transaction, moves the item to purchase history.
- **Purchase history**: archive of previously-bought wishlist items with actual paid amount vs. estimate, useful for retrospective "was this worth it?" reflection.

---

## 16. Split bills & receivables

A standalone primitive (overlaps with travel §14 but not exclusive to it).

- **Split-bill event**: transaction + participants + allocation.
- **Allocation methods**: equal split, by specific amount per person, by percentage, by itemised lines (tie to split-transaction sublines for itemised bills).
- **Receivables ledger**: "Alice owes me 40 CHF from dinner", "I owe Bob 25 CHF from groceries". Aggregates across events.
- **Settlement**: marking a Venmo / Twint / bank transfer as "settles balance with X" closes out the receivable.
- **Per-person view**: running balance with each friend.

---

## 17. Refunds & reimbursements

- **Refund**: negative-amount transaction (income flow back). Auto-link to original purchase based on merchant + amount + date window heuristic. When linked, stats net to zero rather than showing separate +X income / -X expense.
- **Reimbursement**: distinct from refund. Same primitive as split-bill receivable — user records the expense, flags it reimbursable, Folio tracks until marked received.
- **Work expense workflow**: tagging `#reimbursable`, tracking pending amount, filtering by status.

---

## 18. Stats & insights

### Core metrics

- **Total net worth** (all accounts, converted to base currency).
- **Net worth history**: daily snapshots + user-pinned events ("first day of new job", "house down payment made", "market crash"). Auto-detect some events (large inflows, significant balance changes).
- **Savings rate** (liquid): `(income - expenses) / income` per cycle, per year. Assets excluded from numerator.
- **Investment rate**: net investment contributions / income.
- **Runway**: months of expenses your liquid assets cover at average burn.
- **Income / expense / net** per cycle, per month, per year.
- **Category-level trends**: spend per category over time, with period-over-period deltas.
- **Merchant-level trends**: spend at each merchant over time.
- **Tag-level slicing**: spend by tag across categories (e.g. all `#vacation-2026` across categories).
- **Cycle comparisons**: this cycle vs. last, this month vs. same month last year.
- **Flexible date ranges + presets**: this cycle, last cycle, YTD, tax year, all time, custom.

### Visualisations

- Networth line chart (with event markers).
- Cashflow stacked bar (income vs. expense per cycle).
- Category pie / treemap.
- Subscription timeline (when each renews).
- Calendar heatmap (spend intensity).
- Investment allocation pie (current vs. target).

### Drill-downs

- Every chart is clickable → filtered transaction list.
- Every stat is reproducible from underlying data (no magic).

### Second-currency display

- Every amount shown in both account currency and base currency (CHF), using the FX rate on the transaction date.
- Toggleable.
- FX rates stored separately (not baked into amounts); daily rates fetched from ECB.

---

## 19. Calendar view

Unified month-grid view of upcoming financial events:

- Paydays.
- Recurring expenses (rent, subscriptions, insurance).
- Scheduled transfers.
- Planned one-off expenses.
- Trip dates.
- Goal / sinking-fund target dates.
- Corporate action dates (ex-dividend, earnings) for tracked positions.

Interactions:
- Click a date → see all events that day.
- Click an event → jump to its source (recurring template, trip, etc.).
- Toggle event types.
- iCal feed export (optional, post-base).

---

## 20. Search & filters

- **Full-text search** across transactions: memo, notes, merchant, raw bank description, tags, category names.
- **Structured filters**: amount range, date range, account(s), account group(s), category(ies), tag(s), merchant(s), status (planned/scheduled/paid/reconciled), has-attachment, has-receipt.
- **Saved searches** — user saves a filter combination with a name, pin to sidebar.
- **Bulk operations** — select N transactions from search results, bulk re-categorise, bulk tag, bulk delete.

---

## 21. Reports & exports

- **CSV export** of transactions (any filtered view).
- **Monthly PDF summary** — income, expenses, savings rate, top categories, notable events, plan vs. actual recap.
- **Yearly PDF summary** — same, annualised.
- **Tax-year realised gains report** (investments).
- **Wealth tax export** (year-end snapshot of all accounts).
- **Full data export** — all Folio data as JSON + CSV bundle. User-initiated, downloadable.
- **Custom export templates** — user defines columns / filters, saves as a template for reuse.
- **Import-from-Folio** — same JSON bundle re-importable into another Folio instance (migration).

---

## 22. AI features

Opt-in, paid tier. All AI features must work with self-hosted (Ollama-style) backends OR user-supplied API keys (OpenAI / Anthropic). No forced cloud dependency.

### Receipt photo → transaction

- User snaps a receipt photo.
- AI extracts: amount, date, merchant, line items (if visible), currency, tax.
- Creates provisional transaction in the chosen account.
- Line-item extraction automatically fills split transactions.
- Auto-dedupes against the bank-synced transaction when it arrives.

### Categorisation suggestions

- Runs on uncategorised transactions after rules + merchant defaults.
- Suggestion only; user accepts or rejects. User confidence threshold configurable.
- Learns from user corrections (local, no external data sharing beyond the chosen backend).

### Anomaly detection

- Flags unusual transactions (amount, frequency, merchant) relative to user's history.
- Feeds the unusual-spending notification channel.

### Duplicate detection (across sources)

- When same transaction appears in two channels (e.g. manual entry + bank sync + receipt photo), AI-assisted matcher proposes merges.

---

## 23. Attachments & documents

- Attach files to any transaction, any account, any trip, any wishlist item, any goal.
- Supported types: images (receipts), PDFs (invoices, contracts), generic files.
- Preview inline where possible.
- Searchable file metadata.
- OCR on receipts (for AI receipt flow, also for searchability).
- Size limits + storage tracking visible to user (self-hosted = user's own disk).
- Bulk download of all attachments as part of full data export.

---

## 24. Notifications & alerts

### Event library

- Planning reminders (new cycle starting).
- Bill / scheduled transaction due.
- Budget threshold reached (user-configurable thresholds: 50%, 80%, 100%, custom).
- Overrun (category went over budget).
- Unusual spending (see §22 anomaly detection).
- Sync errors (provider connection broken, token expired).
- Goal milestones (% of target reached, target hit).
- Sinking-fund shortfall (next due date approaching, balance below required).
- Recurring cost changed (Netflix went from 17.99 → 19.99).
- Checkpoint drift (account balance diverged from expected).
- Networth events (large inflow, significant balance change — auto-detected).
- Corporate action on held positions (split, merger, delisting, ex-dividend).
- Subscription renewal reminder (X days before).

### User-defined alert rules

Beyond prebaked events, user can define custom rules:
- "When Groceries hits 90% of budget, alert"
- "When Checking balance < 500 CHF, alert"
- "When any single transaction > 500 CHF in category X, alert"
- "When a new merchant is seen for the first time, alert"

### Delivery

- Per-event-type channel preferences (in-app / email / web push / external webhook).
- Digest option for noisy categories (weekly digest instead of real-time).
- External integrations (Telegram / Discord / Slack via webhook) as optional plugins.

---

## 25. Onboarding

- First-run wizard walks user through:
  - Create user account (email, password, optional MFA / passkey).
  - Create the first workspace — name, base currency, cycle anchor, locale, timezone. The creating user becomes the workspace `owner`.
  - Add first account (opening balance + start date).
  - Optionally connect bank via GoCardless / IBKR / CSV import.
  - Review suggested default categories; customise.
  - Set up first income source.
  - Add known recurring expenses.
  - Optional: create first goal.
  - Optional: invite additional members to the workspace (owner or member).
  - Optional: enable AI features.
- Every step skippable; wizard can be re-run later.
- Sample data mode for exploration without real accounts.

---

## 26. Platform & UX

### User-facing

- **Dark mode** (minimum). Optional accent colour. Theming infrastructure for future full themes.
- **Localisation**: English first. Swiss locales (de-CH, fr-CH, it-CH), Portugal as well. Locale and timezone live on the workspace (so all members see the same formatting); UI language can additionally be overridden per user.
- **Number / date formats** — locale-aware (workspace locale), default always EU.
- **Keyboard shortcuts** for power users (jump to accounts, create transaction, global search).
- **Mobile-first responsive** — PWA runs on phones; feature parity where practical.

### Data ownership & control

- **Full data export** (see §21). Scoped per workspace; a user can also export every workspace they belong to in one bundle.
- **Full data delete** — two flavours:
  - **Workspace delete** — wipes a single workspace and all its financial data. Only an `owner` can trigger; soft-delete with a grace period before hard delete.
  - **User account delete** — removes the user identity. Workspaces where the user is the sole owner are deleted with the same grace period; workspaces with other owners simply lose this membership.
- **Data portability** — import/export JSON bundles between Folio instances at workspace granularity.

### Audit log

- Every edit to transactions, budgets, goals, accounts, categories, rules is logged.
- Entries are scoped to the workspace and capture the acting user, so multi-member edits are attributable.
- User can browse the log per entity.
- Read-only; no undo UI in v1 (undo is a future feature).

### Security

- Session cookies + Argon2id passwords.
- Passkeys / WebAuthn supported.
- Provider tokens (GoCardless, IBKR) encrypted at rest (AES-GCM).
- Full-export bundles encrypted with user-provided passphrase (optional).
- 2FA (TOTP) as alternative to passkeys.

### Multi-device / sync

- Server-side data lives in Postgres on the self-hosted instance.
- PWA caches for offline read and offline transaction capture; syncs on reconnect.
- No native apps planned; PWA is the mobile story.

### Accessibility

- Keyboard-navigable everywhere.
- Screen-reader-friendly labels.
- High-contrast mode support.

### Self-host operations

- One-click backup (pg_dump to user-configured target: local disk, S3-compatible, etc.).
- Restore from backup.
- Upgrade path that handles migrations.
- **Platform admin console** for the self-host operator: workspace list with member counts, last activity, deletion state; user list; resource usage. Distinct from workspace-level administration (which any owner has within their own workspace).

---

## Appendix — cross-cutting concepts

For easier lookup:

| Concept | Lives in |
|---|---|
| Workspaces, members, roles, invites | §1 (Workspaces & membership); referenced from §25 (onboarding), §26 (data delete, audit log, admin console) |
| Currency + FX | §1, §2, §4, §18 (second-currency display) |
| Attachments | §23; referenced from §4, §7, §14, §15 |
| AI | §22; referenced from §5 (categorisation), §24 (anomaly) |
| Audit / edit history | §26; referenced from §4 |
| Backfill & checkpoints | §10; referenced from §2 (opening balance), §18 (snapshot recompute) |
| Transfers (matched pairs) | §6; referenced from §2 (CC, mortgage), §9 (goal contributions) |
| Split primitives | split *transactions* in §5, split *bills* in §16 — distinct but may cascade (split bill → split transaction sublines) |

---

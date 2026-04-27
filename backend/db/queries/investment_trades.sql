-- name: InsertInvestmentTrade :one
INSERT INTO investment_trades (
  id, workspace_id, account_id, instrument_id, side,
  quantity, price, currency, fee_amount, fee_currency,
  trade_date, settle_date
) VALUES (
  @id, @workspace_id, @account_id, @instrument_id, @side::trade_side,
  @quantity, @price, @currency, @fee_amount, @fee_currency,
  @trade_date, @settle_date
)
RETURNING
  id, workspace_id, account_id, instrument_id,
  side::text, quantity::text, price::text, currency,
  fee_amount::text, fee_currency, trade_date, settle_date,
  linked_cash_transaction_id, created_at, updated_at;

-- name: InsertInvestmentTradeExec :exec
INSERT INTO investment_trades (
  id, workspace_id, account_id, instrument_id, side,
  quantity, price, currency, fee_amount, fee_currency,
  trade_date, settle_date
) VALUES (
  @id, @workspace_id, @account_id, @instrument_id, @side::trade_side,
  @quantity, @price, @currency, @fee_amount, @fee_currency,
  @trade_date, @settle_date
);

-- name: GetTradeAccountInstrument :one
SELECT account_id, instrument_id FROM investment_trades
WHERE workspace_id = @workspace_id AND id = @id;

-- name: DeleteInvestmentTrade :exec
DELETE FROM investment_trades WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetInvestmentTrade :one
SELECT
  t.id, t.workspace_id, t.account_id, t.instrument_id, i.symbol,
  t.side::text AS side, t.quantity::text AS quantity, t.price::text AS price, t.currency,
  t.fee_amount::text AS fee_amount, t.fee_currency, t.trade_date, t.settle_date,
  t.linked_cash_transaction_id, t.created_at, t.updated_at
FROM investment_trades t
JOIN instruments i ON i.id = t.instrument_id
WHERE t.workspace_id = @workspace_id AND t.id = @id;

-- name: LoadTradeEvents :many
SELECT id, side::text AS side, trade_date, quantity::text AS quantity, price::text AS price, fee_amount::text AS fee_amount, currency
FROM investment_trades
WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id
ORDER BY trade_date ASC, id ASC;

-- name: TradeExists :one
SELECT EXISTS(
  SELECT 1 FROM investment_trades
  WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id
    AND side = @side::trade_side
    AND trade_date = @trade_date
    AND quantity = @quantity
    AND price = @price
);

-- name: ListInvestmentTrades :many
SELECT
  t.id, t.workspace_id, t.account_id, t.instrument_id, i.symbol,
  t.side::text AS side, t.quantity::text AS quantity, t.price::text AS price, t.currency,
  t.fee_amount::text AS fee_amount, t.fee_currency, t.trade_date, t.settle_date,
  t.linked_cash_transaction_id, t.created_at, t.updated_at
FROM investment_trades t
JOIN instruments i ON i.id = t.instrument_id
WHERE t.workspace_id = @workspace_id
  AND (sqlc.narg('account_id')::uuid IS NULL OR t.account_id = sqlc.narg('account_id')::uuid)
  AND (sqlc.narg('instrument_id')::uuid IS NULL OR t.instrument_id = sqlc.narg('instrument_id')::uuid)
ORDER BY t.trade_date DESC, t.id DESC
LIMIT 1000;

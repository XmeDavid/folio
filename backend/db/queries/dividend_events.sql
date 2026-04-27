-- name: InsertDividendEvent :one
INSERT INTO dividend_events (
  id, workspace_id, account_id, instrument_id,
  ex_date, pay_date, amount_per_unit, currency, total_amount, tax_withheld
) VALUES (
  @id, @workspace_id, @account_id, @instrument_id,
  @ex_date, @pay_date, @amount_per_unit, @currency, @total_amount, @tax_withheld
)
RETURNING
  id, workspace_id, account_id, instrument_id,
  ex_date, pay_date, amount_per_unit::text, currency,
  total_amount::text, tax_withheld::text,
  linked_cash_transaction_id, created_at;

-- name: InsertDividendEventExec :exec
INSERT INTO dividend_events (
  id, workspace_id, account_id, instrument_id,
  ex_date, pay_date, amount_per_unit, currency, total_amount, tax_withheld
) VALUES (
  @id, @workspace_id, @account_id, @instrument_id,
  @ex_date, @pay_date, @amount_per_unit, @currency, @total_amount, @tax_withheld
);

-- name: GetDividendAccountInstrument :one
SELECT account_id, instrument_id FROM dividend_events
WHERE workspace_id = @workspace_id AND id = @id;

-- name: DeleteDividendEvent :exec
DELETE FROM dividend_events WHERE workspace_id = @workspace_id AND id = @id;

-- name: LoadDividendEvents :many
SELECT pay_date, total_amount::text, currency
FROM dividend_events
WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id
ORDER BY pay_date ASC, id ASC;

-- name: DividendExists :one
SELECT EXISTS(
  SELECT 1 FROM dividend_events
  WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id
    AND pay_date = @pay_date
    AND total_amount = @total_amount
);

-- name: ListInvestmentDividends :many
SELECT
  d.id, d.workspace_id, d.account_id, d.instrument_id, i.symbol,
  d.ex_date, d.pay_date, d.amount_per_unit::text AS amount_per_unit, d.currency,
  d.total_amount::text AS total_amount, d.tax_withheld::text AS tax_withheld,
  d.linked_cash_transaction_id, d.created_at
FROM dividend_events d
JOIN instruments i ON i.id = d.instrument_id
WHERE d.workspace_id = @workspace_id
  AND (sqlc.narg('account_id')::uuid IS NULL OR d.account_id = sqlc.narg('account_id')::uuid)
  AND (sqlc.narg('instrument_id')::uuid IS NULL OR d.instrument_id = sqlc.narg('instrument_id')::uuid)
ORDER BY d.pay_date DESC, d.id DESC
LIMIT 1000;

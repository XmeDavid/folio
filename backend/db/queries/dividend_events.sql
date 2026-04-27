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

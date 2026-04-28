-- name: DeleteInvestmentPosition :exec
DELETE FROM investment_positions
WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id;

-- name: UpsertInvestmentPosition :exec
INSERT INTO investment_positions AS ip (
  account_id, instrument_id, workspace_id,
  quantity, average_cost, realised_pnl, currency, refreshed_at
) VALUES (@account_id, @instrument_id, @workspace_id, @quantity, @average_cost, @realised_pnl, @currency, @refreshed_at)
ON CONFLICT (account_id, instrument_id) DO UPDATE SET
  quantity = excluded.quantity,
  average_cost = excluded.average_cost,
  realised_pnl = excluded.realised_pnl,
  currency = excluded.currency,
  refreshed_at = excluded.refreshed_at;

-- name: DeleteLotConsumptionsForPosition :exec
DELETE FROM investment_lot_consumptions lc
WHERE lc.workspace_id = @workspace_id
  AND lc.lot_id IN (
    SELECT l.id FROM investment_lots l
    WHERE l.workspace_id = @workspace_id AND l.account_id = @account_id AND l.instrument_id = @instrument_id
  );

-- name: DeleteLotsForPosition :exec
DELETE FROM investment_lots WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id;

-- name: InsertInvestmentLot :exec
INSERT INTO investment_lots (
  id, workspace_id, account_id, instrument_id,
  acquired_at, quantity_opening, quantity_remaining,
  cost_basis_per_unit, currency, source_trade_id, closed_at
) VALUES (@id, @workspace_id, @account_id, @instrument_id,
  @acquired_at, @quantity_opening, @quantity_remaining,
  @cost_basis_per_unit, @currency, @source_trade_id, @closed_at);

-- name: InsertLotConsumption :exec
INSERT INTO investment_lot_consumptions (
  id, workspace_id, lot_id, sell_trade_id,
  quantity_consumed, realised_gain, currency, consumed_at
) VALUES (@id, @workspace_id, @lot_id, @sell_trade_id,
  @quantity_consumed, @realised_gain, @currency, @consumed_at);

-- name: ListOpenPositionInstruments :many
SELECT DISTINCT i.id, i.symbol
FROM investment_positions p
JOIN instruments i ON i.id = p.instrument_id
WHERE p.workspace_id = @workspace_id AND p.quantity > 0;

-- name: ListOpenPositionInstrumentsWithPrice :many
SELECT DISTINCT ON (p.instrument_id)
  p.instrument_id, i.symbol,
  coalesce(lp.as_of, '0001-01-01 00:00:00+00'::timestamptz) AS last_price_as_of
FROM investment_positions p
JOIN instruments i ON i.id = p.instrument_id
LEFT JOIN LATERAL (
  SELECT as_of FROM instrument_prices
  WHERE instrument_id = p.instrument_id
  ORDER BY as_of DESC
  LIMIT 1
) lp ON true
WHERE p.workspace_id = @workspace_id AND p.quantity > 0 AND i.active
ORDER BY p.instrument_id;

-- name: ListTouchedInvestmentPairs :many
SELECT DISTINCT account_id, instrument_id
FROM investment_trades
WHERE investment_trades.workspace_id = sqlc.arg(workspace_id)
UNION
SELECT DISTINCT account_id, instrument_id
FROM dividend_events
WHERE dividend_events.workspace_id = sqlc.arg(workspace_id);

-- name: ListInvestmentPositions :many
SELECT
  p.account_id, p.instrument_id, p.workspace_id,
  i.symbol, i.name, i.asset_class::text AS asset_class, i.currency AS instrument_currency,
  a.currency AS account_currency,
  p.quantity::text AS quantity, p.average_cost::text AS average_cost,
  (p.quantity * p.average_cost)::text AS cost_basis_total,
  p.realised_pnl::text AS realised_pnl,
  coalesce(div.total, 0)::text AS dividends_received,
  coalesce(fees.total, 0)::text AS fees_paid,
  last_trade.last_date AS last_trade_date,
  coalesce(lp.price::text, '') AS last_price,
  coalesce(lp.as_of, '0001-01-01 00:00:00+00'::timestamptz) AS last_price_at
FROM investment_positions p
JOIN instruments i ON i.id = p.instrument_id
JOIN accounts a ON a.id = p.account_id
LEFT JOIN LATERAL (
  SELECT coalesce(sum(total_amount), 0) AS total
  FROM dividend_events d
  WHERE d.workspace_id = p.workspace_id
    AND d.account_id = p.account_id
    AND d.instrument_id = p.instrument_id
) div ON true
LEFT JOIN LATERAL (
  SELECT coalesce(sum(fee_amount), 0) AS total
  FROM investment_trades t
  WHERE t.workspace_id = p.workspace_id
    AND t.account_id = p.account_id
    AND t.instrument_id = p.instrument_id
) fees ON true
LEFT JOIN LATERAL (
  SELECT max(trade_date) AS last_date
  FROM investment_trades t
  WHERE t.workspace_id = p.workspace_id
    AND t.account_id = p.account_id
    AND t.instrument_id = p.instrument_id
) last_trade ON true
LEFT JOIN LATERAL (
  SELECT price, as_of
  FROM instrument_prices
  WHERE instrument_id = p.instrument_id
  ORDER BY as_of DESC
  LIMIT 1
) lp ON true
WHERE p.workspace_id = @workspace_id
  AND (sqlc.narg('account_id')::uuid IS NULL OR p.account_id = sqlc.narg('account_id')::uuid)
  AND (sqlc.narg('instrument_id')::uuid IS NULL OR p.instrument_id = sqlc.narg('instrument_id')::uuid)
  AND (NOT sqlc.arg(open_only)::bool OR p.quantity > 0)
  AND (NOT sqlc.arg(closed_only)::bool OR p.quantity = 0)
  AND (
    sqlc.arg(search)::text = ''
    OR lower(i.symbol) LIKE sqlc.arg(search_pattern)::text
    OR lower(i.name) LIKE sqlc.arg(search_pattern)::text
  )
ORDER BY (p.quantity * coalesce(lp.price, p.average_cost)) DESC, i.symbol;

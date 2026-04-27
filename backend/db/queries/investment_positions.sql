-- name: DeleteInvestmentPosition :exec
DELETE FROM investment_positions
WHERE workspace_id = @workspace_id AND account_id = @account_id AND instrument_id = @instrument_id;

-- name: UpsertInvestmentPosition :exec
INSERT INTO investment_positions AS ip (
  account_id, instrument_id, workspace_id,
  quantity, average_cost, currency, refreshed_at
) VALUES (@account_id, @instrument_id, @workspace_id, @quantity, @average_cost, @currency, @refreshed_at)
ON CONFLICT (account_id, instrument_id) DO UPDATE SET
  quantity = excluded.quantity,
  average_cost = excluded.average_cost,
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
  p.instrument_id, i.symbol, lp.as_of::timestamptz AS last_price_as_of
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

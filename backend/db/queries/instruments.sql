-- name: FindInstrumentByISIN :one
SELECT id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at
FROM instruments WHERE isin = @isin;

-- name: FindInstrumentBySymbolAndExchange :one
SELECT id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at
FROM instruments WHERE symbol = @symbol AND exchange = @exchange AND isin IS NULL;

-- name: FindInstrumentBySymbolOnly :one
SELECT id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at
FROM instruments WHERE symbol = @symbol AND exchange IS NULL AND isin IS NULL
LIMIT 1;

-- name: InsertInstrument :one
INSERT INTO instruments (id, symbol, isin, name, asset_class, currency, exchange)
VALUES (@id, @symbol, @isin, @name, @asset_class::asset_class, @currency, @exchange)
ON CONFLICT DO NOTHING
RETURNING id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at;

-- name: GetInstrumentByID :one
SELECT id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at
FROM instruments WHERE id = @id;

-- name: GetInstrumentBySymbol :one
SELECT id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at
FROM instruments WHERE upper(symbol) = upper(@symbol)
ORDER BY active DESC, updated_at DESC
LIMIT 1;

-- name: SearchInstruments :many
SELECT id, symbol, isin, name, asset_class::text AS asset_class, currency, exchange, active, created_at, updated_at
FROM instruments
WHERE active AND (symbol ILIKE @query || '%' OR name ILIKE '%' || @query || '%' OR isin ILIKE @query || '%')
ORDER BY CASE WHEN upper(symbol) = upper(@query) THEN 0 ELSE 1 END, symbol
LIMIT @max_results;

-- name: GetInstrumentCurrency :one
SELECT currency FROM instruments WHERE id = @id;

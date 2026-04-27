-- name: LookupCachedFXRate :one
SELECT rate
FROM fx_rates
WHERE base_currency = @base_currency AND quote_currency = @quote_currency AND as_of <= @as_of
ORDER BY as_of DESC
LIMIT 1;

-- name: PersistFXRate :exec
INSERT INTO fx_rates (id, base_currency, quote_currency, as_of, rate, source)
VALUES (@id, @base_currency, @quote_currency, @as_of, @rate, @source::fx_source)
ON CONFLICT (base_currency, quote_currency, as_of, source) DO NOTHING;

-- name: LookupCachedLatestPrice :one
SELECT as_of, price, currency, source::text AS source
FROM instrument_prices
WHERE instrument_id = @instrument_id
ORDER BY as_of DESC
LIMIT 1;

-- name: LookupCachedPriceRange :many
SELECT as_of, price, currency, source::text AS source
FROM instrument_prices
WHERE instrument_id = @instrument_id AND as_of >= @from_date AND as_of <= @to_date
ORDER BY as_of ASC;

-- name: PersistInstrumentPrice :exec
INSERT INTO instrument_prices (id, instrument_id, as_of, price, currency, source)
VALUES (@id, @instrument_id, @as_of, @price, @currency, @source::price_source)
ON CONFLICT (instrument_id, as_of, source) DO NOTHING;

-- name: GetTransaction :one
SELECT id, workspace_id, account_id, status::text, booked_at, value_at, posted_at,
       amount::text, currency, original_amount::text, original_currency::text,
       merchant_id, category_id, counterparty_raw, description, notes,
       count_as_expense, created_at, updated_at
FROM transactions
WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertTransaction :one
INSERT INTO transactions (
  id, workspace_id, account_id, status, booked_at, value_at, posted_at,
  amount, currency, merchant_id, category_id,
  counterparty_raw, description, notes, count_as_expense
) VALUES (
  @id, @workspace_id, @account_id, @status::transaction_status, @booked_at, @value_at, @posted_at,
  @amount::numeric, @currency, @merchant_id, @category_id,
  @counterparty_raw, @description, @notes, @count_as_expense
)
RETURNING id, workspace_id, account_id, status::text, booked_at, value_at, posted_at,
          amount::text, currency, original_amount::text, original_currency::text,
          merchant_id, category_id, counterparty_raw, description, notes,
          count_as_expense, created_at, updated_at;

-- name: DeleteTransaction :execrows
DELETE FROM transactions WHERE workspace_id = @workspace_id AND id = @id;

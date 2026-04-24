// Package gocardless integrates with GoCardless Bank Account Data (ex-Nordigen).
// Read-only: requisitions → consents → accounts → balances/transactions.
//
// Docs: https://developer.gocardless.com/bank-account-data
//
// Stored secrets (encrypted at rest via internal/crypto):
//   - requisition_id (per end-user connection)
//   - refresh token if applicable
//
// Sync model:
//   - Cron job (River) every N hours pulls new transactions per account.
//   - Transactions are upserted by provider-supplied transaction_id.
package gocardless

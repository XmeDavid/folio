// Package jobs hosts background workers powered by River (Postgres-backed queue).
//
// Planned workers:
//   - GoCardlessSyncWorker   — refresh accounts & transactions
//   - IBKRFlexSyncWorker     — fetch Flex statements
//   - FXRateFetchWorker      — pull daily FX rates for reporting
//   - ReconciliationWorker   — detect duplicates, balance drift
//   - NotificationWorker     — email/push on anomalies, low balance, etc.
//
// Docs: https://riverqueue.com
package jobs

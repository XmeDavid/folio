// Package ibkr integrates with Interactive Brokers Flex Web Service.
//
// Two-step flow:
//  1. POST SendRequest with (FlexQueryId, Token) → returns ReferenceCode.
//  2. GET GetStatement with (ReferenceCode, Token) → returns XML report.
//
// Per-user secrets (encrypted at rest):
//   - flex_token
//   - flex_query_id
//
// Docs: https://www.interactivebrokers.com/campus/ibkr-api-page/flex-web-service/
package ibkr

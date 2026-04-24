// Package camt053 parses ISO-20022 camt.053 bank statement XML files.
//
// This is the Swiss/EU standard for account statements and is the primary
// import path for PostFinance and many other Swiss banks that don't expose
// a consumer-facing API.
//
// Usage:
//
//	stmt, err := camt053.Parse(reader)
//	for _, tx := range stmt.Transactions { ... }
package camt053

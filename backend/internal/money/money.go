// Package money provides decimal-safe money and currency primitives.
//
// Never use float64 for monetary values. Use Amount (github.com/shopspring/decimal)
// and always pair amounts with a Currency. FX conversion is explicit.
package money

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Amount is an alias for decimal.Decimal to keep our types explicit.
type Amount = decimal.Decimal

// Currency is an ISO-4217 code (uppercase). Crypto codes (BTC, ETH) are also valid
// but must not collide with ISO-4217. The backend stores currency as TEXT.
type Currency string

func (c Currency) String() string { return string(c) }

func (c Currency) Valid() bool {
	s := string(c)
	if len(s) < 3 || len(s) > 8 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// ParseCurrency normalises and validates a currency code.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(strings.ToUpper(strings.TrimSpace(s)))
	if !c.Valid() {
		return "", fmt.Errorf("invalid currency code: %q", s)
	}
	return c, nil
}

// Money pairs an Amount with its Currency. This is the value type that should
// cross package boundaries — never a bare Amount.
type Money struct {
	Amount   Amount
	Currency Currency
}

func New(a Amount, c Currency) Money { return Money{Amount: a, Currency: c} }

func FromString(amount string, currency string) (Money, error) {
	a, err := decimal.NewFromString(amount)
	if err != nil {
		return Money{}, fmt.Errorf("amount: %w", err)
	}
	c, err := ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: a, Currency: c}, nil
}

func (m Money) String() string {
	return fmt.Sprintf("%s %s", m.Amount.String(), m.Currency)
}

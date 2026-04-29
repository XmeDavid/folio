package bankimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestDateProximityMatch(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }
	cases := []struct {
		name           string
		incomingBooked time.Time
		existingBooked time.Time
		existingPosted *time.Time
		toleranceDays  int
		wantMatch      bool
	}{
		{"same day", day("2025-12-16"), day("2025-12-16"), nil, 1, true},
		{"one day off booked", day("2025-12-15"), day("2025-12-16"), nil, 1, true},
		{"two days off booked", day("2025-12-14"), day("2025-12-16"), nil, 1, false},
		{"matches existing posted", day("2025-12-16"), day("2025-12-10"), postedPtr("2025-12-16"), 1, true},
		{"posted one day off", day("2025-12-17"), day("2025-12-10"), postedPtr("2025-12-16"), 1, true},
		{"both far away", day("2025-12-01"), day("2025-12-10"), postedPtr("2025-12-16"), 1, false},
		{"7d window catches gap", day("2025-12-09"), day("2025-12-16"), nil, 7, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dateProximityMatch(tc.incomingBooked, nil, tc.existingBooked, tc.existingPosted, tc.toleranceDays)
			if got != tc.wantMatch {
				t.Fatalf("dateProximityMatch = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}

func TestFuzzyStableMatchAutoWindow(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	postedPtr := func(s string) *time.Time { p := day(s); return &p }
	desc := "Amazon"
	existing := existingTx{
		ID:          uuid.New(),
		BookedAt:    day("2025-12-10"),
		PostedAt:    postedPtr("2025-12-16"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: "Amazon",
	}
	cases := []struct {
		name      string
		incoming  ParsedTransaction
		wantMatch bool
	}{
		{
			name: "amazon settle vs auth — auto match via posted",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-16"),
				Amount:      decimal.RequireFromString("-152.98"),
				Currency:    "CHF",
				Description: &desc,
			},
			wantMatch: true,
		},
		{
			name: "different amount no match",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-16"),
				Amount:      decimal.RequireFromString("-152.99"),
				Currency:    "CHF",
				Description: &desc,
			},
			wantMatch: false,
		},
		{
			name: "different currency no match",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-16"),
				Amount:      decimal.RequireFromString("-152.98"),
				Currency:    "EUR",
				Description: &desc,
			},
			wantMatch: false,
		},
		{
			name: "8 days off — outside auto window",
			incoming: ParsedTransaction{
				BookedAt:    day("2025-12-24"),
				Amount:      decimal.RequireFromString("-152.98"),
				Currency:    "CHF",
				Description: &desc,
			},
			wantMatch: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fuzzyStableMatch(tc.incoming, existing, autoDedupDays); got != tc.wantMatch {
				t.Fatalf("fuzzyStableMatch = %v, want %v", got, tc.wantMatch)
			}
		})
	}
}

func TestConflictByStableFieldsDateDrift(t *testing.T) {
	day := func(s string) time.Time {
		t, _ := time.Parse("2006-01-02", s)
		return t
	}
	desc := "Amazon"
	existing := []existingTx{{
		ID:          uuid.New(),
		BookedAt:    day("2025-12-09"),
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: "Amazon",
	}}
	incoming := ParsedTransaction{
		BookedAt:    day("2025-12-15"), // 6 days off — outside auto, inside review
		Amount:      decimal.RequireFromString("-152.98"),
		Currency:    "CHF",
		Description: &desc,
	}
	got, ok := conflictByStableFields(incoming, existing)
	if !ok {
		t.Fatal("expected a conflict for 6-day drift")
	}
	if got.Reason != "date_drift" {
		t.Fatalf("conflict reason = %q, want date_drift", got.Reason)
	}
}

func TestNormalizeDescription(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Amazon", "amazon"},
		{"  AMAZON  ", "amazon"},
		{"Amazon.com", "amazoncom"},
		{"Pagamento com cartão Amazon", "pagamento com cartao amazon"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := normalizeDescription(tc.in); got != tc.want {
				t.Fatalf("normalizeDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Package accounts owns the Folio "account" aggregate: an account row plus
// its append-only balance-snapshot timeline. Account creation always writes
// an opening snapshot, in the same transaction, so every account has a
// derivable balance from day one.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
	"github.com/xmedavid/folio/backend/internal/httpx"
	"github.com/xmedavid/folio/backend/internal/money"
	"github.com/xmedavid/folio/backend/internal/uuidx"
)

// validKinds mirrors the account_kind enum in db/migrations.
var validKinds = map[string]bool{
	"checking": true, "savings": true, "cash": true, "credit_card": true,
	"brokerage": true, "crypto_wallet": true, "loan": true, "mortgage": true,
	"asset": true, "pillar_2": true, "pillar_3a": true, "other": true,
}

// defaultIncludeInSavingsRate is true for liquid spendable balances and
// false for every other kind. POST can override.
func defaultIncludeInSavingsRate(kind string) bool {
	switch kind {
	case "checking", "savings", "cash":
		return true
	}
	return false
}

// Account is the read-model representation returned by the API. Balance is
// the value of the latest snapshot; for newly-created accounts that is the
// opening balance.
type Account struct {
	ID                   uuid.UUID  `json:"id"`
	WorkspaceID          uuid.UUID  `json:"workspaceId"`
	Name                 string     `json:"name"`
	Nickname             *string    `json:"nickname,omitempty"`
	Kind                 string     `json:"kind"`
	Currency             string     `json:"currency"`
	Institution          *string    `json:"institution,omitempty"`
	AccountGroupID       *uuid.UUID `json:"accountGroupId,omitempty"`
	AccountSortOrder     int        `json:"accountSortOrder"`
	OpenDate             time.Time  `json:"openDate"`
	CloseDate            *time.Time `json:"closeDate,omitempty"`
	OpeningBalance       string     `json:"openingBalance"`
	OpeningBalanceDate   time.Time  `json:"openingBalanceDate"`
	IncludeInNetworth    bool       `json:"includeInNetworth"`
	IncludeInSavingsRate bool       `json:"includeInSavingsRate"`
	ArchivedAt           *time.Time `json:"archivedAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	Balance              string     `json:"balance"`
	BalanceAsOf          *time.Time `json:"balanceAsOf,omitempty"`
}

// CreateInput is the validated input to Create.
type CreateInput struct {
	Name                 string
	Nickname             *string
	Kind                 string
	Currency             string
	Institution          *string
	AccountGroupID       *uuid.UUID
	OpenDate             time.Time
	OpeningBalance       decimal.Decimal
	OpeningBalanceDate   *time.Time
	IncludeInNetworth    *bool
	IncludeInSavingsRate *bool
}

// normalize trims, validates, and applies defaults to a CreateInput. Pure
// function — tested without the database.
func (in CreateInput) normalize() (CreateInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if in.Name == "" {
		return in, httpx.NewValidationError("name is required")
	}
	if !validKinds[in.Kind] {
		return in, httpx.NewValidationError(fmt.Sprintf("kind %q is not a valid account_kind", in.Kind))
	}
	cur, err := money.ParseCurrency(in.Currency)
	if err != nil {
		return in, httpx.NewValidationError(err.Error())
	}
	in.Currency = string(cur)
	if in.OpenDate.IsZero() {
		return in, httpx.NewValidationError("openDate is required")
	}
	if in.OpeningBalanceDate == nil {
		d := in.OpenDate
		in.OpeningBalanceDate = &d
	}
	if in.IncludeInNetworth == nil {
		t := true
		in.IncludeInNetworth = &t
	}
	if in.IncludeInSavingsRate == nil {
		def := defaultIncludeInSavingsRate(in.Kind)
		in.IncludeInSavingsRate = &def
	}
	return in, nil
}

// PatchInput is the validated input to Update. Pointer fields mean
// "absent" when nil; an explicit empty string on a *string field means
// "set to NULL". For `Archived`: true→archive (archived_at = now()),
// false→unarchive (archived_at = NULL).
type PatchInput struct {
	Name                 *string
	Nickname             *string
	Kind                 *string
	Institution          *string
	AccountGroupID       **uuid.UUID
	AccountSortOrder     *int
	IncludeInNetworth    *bool
	IncludeInSavingsRate *bool
	CloseDate            *string // RFC3339 date; "" to clear
	Archived             *bool
}

// normalize validates any provided PATCH fields. Pure function.
func (in PatchInput) normalize() (PatchInput, error) {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return in, httpx.NewValidationError("name cannot be empty")
	}
	if in.Kind != nil {
		k := strings.ToLower(strings.TrimSpace(*in.Kind))
		if !validKinds[k] {
			return in, httpx.NewValidationError(fmt.Sprintf("kind %q is not a valid account_kind", *in.Kind))
		}
		*in.Kind = k
	}
	if in.CloseDate != nil && *in.CloseDate != "" {
		if _, err := time.Parse("2006-01-02", *in.CloseDate); err != nil {
			return in, httpx.NewValidationError("closeDate must be YYYY-MM-DD")
		}
	}
	return in, nil
}

// PositionValuator returns the market value of open investment positions per
// account in each account's own currency. Implemented by investments.Service.
// Used so account balances on the dashboard / accounts list reflect the value
// of holdings, not just cash.
type PositionValuator interface {
	MarketValueByAccount(ctx context.Context, workspaceID uuid.UUID) (map[uuid.UUID]decimal.Decimal, error)
}

// Service is the accounts service.
type Service struct {
	pool      *pgxpool.Pool
	now       func() time.Time
	positions PositionValuator
}

// NewService returns a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// SetPositionValuator wires an investments-side valuator. When set, List/Get
// add the workspace's per-account market value to the derived cash balance.
func (s *Service) SetPositionValuator(p PositionValuator) { s.positions = p }

type AccountGroup struct {
	ID                uuid.UUID  `json:"id"`
	WorkspaceID       uuid.UUID  `json:"workspaceId"`
	Name              string     `json:"name"`
	SortOrder         int        `json:"sortOrder"`
	AggregateBalances bool       `json:"aggregateBalances"`
	ArchivedAt        *time.Time `json:"archivedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type CreateGroupInput struct {
	Name              string
	AggregateBalances bool
}

type PatchGroupInput struct {
	Name              *string
	SortOrder         *int
	AggregateBalances *bool
	Archived          *bool
}

type GroupOrderInput struct {
	ID        uuid.UUID
	SortOrder int
}

type AccountOrderInput struct {
	ID             uuid.UUID
	AccountGroupID *uuid.UUID
	SortOrder      int
}

type ReorderInput struct {
	Groups   []GroupOrderInput
	Accounts []AccountOrderInput
}

// decimalToNumeric converts a decimal.Decimal to pgtype.Numeric for sqlc params.
func decimalToNumeric(d decimal.Decimal) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(d.String())
	return n
}

// toTimePtr converts an interface{} (from sqlc's untyped CASE result) to
// *time.Time, returning nil when the database value is NULL.
func toTimePtr(v interface{}) *time.Time {
	if v == nil {
		return nil
	}
	if t, ok := v.(time.Time); ok {
		return &t
	}
	return nil
}

func groupRowToModel(r dbq.GetAccountGroupRow) AccountGroup {
	return AccountGroup{
		ID:                r.ID,
		WorkspaceID:       r.WorkspaceID,
		Name:              r.Name,
		SortOrder:         int(r.SortOrder),
		AggregateBalances: r.AggregateBalances,
		ArchivedAt:        r.ArchivedAt,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func (s *Service) ListGroups(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]AccountGroup, error) {
	q := dbq.New(s.pool)
	out := make([]AccountGroup, 0)
	if includeArchived {
		rows, err := q.ListAccountGroups(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("query account groups: %w", err)
		}
		for _, r := range rows {
			out = append(out, AccountGroup{
				ID: r.ID, WorkspaceID: r.WorkspaceID, Name: r.Name,
				SortOrder: int(r.SortOrder), AggregateBalances: r.AggregateBalances,
				ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
		}
	} else {
		rows, err := q.ListAccountGroupsActive(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("query account groups: %w", err)
		}
		for _, r := range rows {
			out = append(out, AccountGroup{
				ID: r.ID, WorkspaceID: r.WorkspaceID, Name: r.Name,
				SortOrder: int(r.SortOrder), AggregateBalances: r.AggregateBalances,
				ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
		}
	}
	return out, nil
}

func (s *Service) CreateGroup(ctx context.Context, workspaceID uuid.UUID, raw CreateGroupInput) (*AccountGroup, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		return nil, httpx.NewValidationError("name is required")
	}

	row, err := dbq.New(s.pool).InsertAccountGroup(ctx, dbq.InsertAccountGroupParams{
		ID:                uuidx.New(),
		WorkspaceID:       workspaceID,
		Name:              name,
		AggregateBalances: raw.AggregateBalances,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, httpx.NewValidationError("account group name already exists")
		}
		return nil, fmt.Errorf("insert account group: %w", err)
	}
	g := &AccountGroup{
		ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name,
		SortOrder: int(row.SortOrder), AggregateBalances: row.AggregateBalances,
		ArchivedAt: row.ArchivedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	return g, nil
}

func (s *Service) UpdateGroup(ctx context.Context, workspaceID, groupID uuid.UUID, in PatchGroupInput) (*AccountGroup, error) {
	sets := make([]string, 0, 3)
	args := []any{workspaceID, groupID}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, httpx.NewValidationError("name cannot be empty")
		}
		sets = append(sets, "name = "+next(name))
	}
	if in.SortOrder != nil {
		sets = append(sets, "sort_order = "+next(*in.SortOrder))
	}
	if in.AggregateBalances != nil {
		sets = append(sets, "aggregate_balances = "+next(*in.AggregateBalances))
	}
	if in.Archived != nil {
		if *in.Archived {
			sets = append(sets, "archived_at = "+next(s.now().UTC()))
		} else {
			sets = append(sets, "archived_at = "+next(nil))
		}
	}
	if len(sets) == 0 {
		return s.GetGroup(ctx, workspaceID, groupID)
	}

	var g AccountGroup
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		update account_groups set %s
		where workspace_id = $1 and id = $2
		returning id, workspace_id, name, sort_order, aggregate_balances, archived_at, created_at, updated_at
	`, strings.Join(sets, ", ")), args...).Scan(
		&g.ID, &g.WorkspaceID, &g.Name, &g.SortOrder, &g.AggregateBalances, &g.ArchivedAt, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account group")
		}
		if isUniqueViolation(err) {
			return nil, httpx.NewValidationError("account group name already exists")
		}
		return nil, fmt.Errorf("update account group: %w", err)
	}
	return &g, nil
}

func (s *Service) GetGroup(ctx context.Context, workspaceID, groupID uuid.UUID) (*AccountGroup, error) {
	row, err := dbq.New(s.pool).GetAccountGroup(ctx, dbq.GetAccountGroupParams{
		WorkspaceID: workspaceID,
		ID:          groupID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account group")
		}
		return nil, err
	}
	g := groupRowToModel(row)
	return &g, nil
}

func (s *Service) DeleteGroup(ctx context.Context, workspaceID, groupID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete account group: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	if err := q.ClearAccountGroupMembership(ctx, dbq.ClearAccountGroupMembershipParams{
		WorkspaceID: workspaceID,
		GroupID:     groupID,
	}); err != nil {
		return fmt.Errorf("clear account group membership: %w", err)
	}
	n, err := q.DeleteAccountGroup(ctx, dbq.DeleteAccountGroupParams{
		WorkspaceID: workspaceID,
		ID:          groupID,
	})
	if err != nil {
		return fmt.Errorf("delete account group: %w", err)
	}
	if n == 0 {
		return httpx.NewNotFoundError("account group")
	}
	return tx.Commit(ctx)
}

func (s *Service) Reorder(ctx context.Context, workspaceID uuid.UUID, in ReorderInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reorder accounts: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	for _, group := range in.Groups {
		n, err := q.ReorderAccountGroup(ctx, dbq.ReorderAccountGroupParams{
			WorkspaceID: workspaceID,
			ID:          group.ID,
			SortOrder:   int32(group.SortOrder),
		})
		if err != nil {
			return fmt.Errorf("reorder account group: %w", err)
		}
		if n == 0 {
			return httpx.NewNotFoundError("account group")
		}
	}

	for _, account := range in.Accounts {
		n, err := q.ReorderAccount(ctx, dbq.ReorderAccountParams{
			WorkspaceID:      workspaceID,
			ID:               account.ID,
			AccountGroupID:   account.AccountGroupID,
			AccountSortOrder: int32(account.SortOrder),
		})
		if err != nil {
			return fmt.Errorf("reorder account: %w", err)
		}
		if n == 0 {
			return httpx.NewNotFoundError("account")
		}
	}

	return tx.Commit(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Create inserts an account and its opening balance snapshot in one tx.
// workspaceID MUST be taken from request context — never from the request body.
func (s *Service) Create(ctx context.Context, workspaceID uuid.UUID, raw CreateInput) (*Account, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	accountID := uuidx.New()
	snapshotID := uuidx.New()

	// timestamptz for the opening snapshot: use midnight UTC of the
	// opening_balance_date so the snapshot sorts correctly on the timeline.
	openingTS := time.Date(
		in.OpeningBalanceDate.Year(),
		in.OpeningBalanceDate.Month(),
		in.OpeningBalanceDate.Day(),
		0, 0, 0, 0, time.UTC,
	)
	openingBalanceStr := in.OpeningBalance.String()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	row, err := q.InsertAccount(ctx, dbq.InsertAccountParams{
		ID:                   accountID,
		WorkspaceID:          workspaceID,
		Name:                 in.Name,
		Nickname:             in.Nickname,
		Kind:                 dbq.AccountKind(in.Kind),
		Currency:             in.Currency,
		Institution:          in.Institution,
		AccountGroupID:       in.AccountGroupID,
		OpenDate:             in.OpenDate,
		OpeningBalance:       decimalToNumeric(in.OpeningBalance),
		OpeningBalanceDate:   *in.OpeningBalanceDate,
		IncludeInNetworth:    *in.IncludeInNetworth,
		IncludeInSavingsRate: *in.IncludeInSavingsRate,
	})
	if err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}

	if err := q.InsertOpeningSnapshot(ctx, dbq.InsertOpeningSnapshotParams{
		ID:          snapshotID,
		WorkspaceID: workspaceID,
		AccountID:   accountID,
		AsOf:        openingTS,
		Balance:     decimalToNumeric(in.OpeningBalance),
		Currency:    in.Currency,
	}); err != nil {
		return nil, fmt.Errorf("insert opening snapshot: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	acc := &Account{
		ID:                   row.ID,
		WorkspaceID:          row.WorkspaceID,
		Name:                 row.Name,
		Nickname:             row.Nickname,
		Kind:                 row.Kind,
		Currency:             row.Currency,
		Institution:          row.Institution,
		AccountGroupID:       row.AccountGroupID,
		AccountSortOrder:     int(row.AccountSortOrder),
		OpenDate:             row.OpenDate,
		CloseDate:            row.CloseDate,
		OpeningBalance:       row.OpeningBalance,
		OpeningBalanceDate:   row.OpeningBalanceDate,
		IncludeInNetworth:    row.IncludeInNetworth,
		IncludeInSavingsRate: row.IncludeInSavingsRate,
		ArchivedAt:           row.ArchivedAt,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		Balance:              openingBalanceStr,
		BalanceAsOf:          &openingTS,
	}
	return acc, nil
}

// balanceRowToAccount converts a sqlc derived-balance row to an Account model.
// The BalanceAsOf field is interface{} because the CASE expression can return
// NULL; toTimePtr handles the type assertion.
func balanceRowToAccount(
	id, workspaceID uuid.UUID, name string, nickname *string, kind, currency string,
	institution *string, accountGroupID *uuid.UUID, accountSortOrder int32,
	openDate time.Time, closeDate *time.Time, openingBalance string, openingBalanceDate time.Time,
	includeInNetworth, includeInSavingsRate bool, archivedAt *time.Time,
	createdAt, updatedAt time.Time, balanceAsOf interface{}, balance string,
) Account {
	return Account{
		ID: id, WorkspaceID: workspaceID, Name: name, Nickname: nickname,
		Kind: kind, Currency: currency, Institution: institution,
		AccountGroupID: accountGroupID, AccountSortOrder: int(accountSortOrder),
		OpenDate: openDate, CloseDate: closeDate,
		OpeningBalance: openingBalance, OpeningBalanceDate: openingBalanceDate,
		IncludeInNetworth: includeInNetworth, IncludeInSavingsRate: includeInSavingsRate,
		ArchivedAt: archivedAt, CreatedAt: createdAt, UpdatedAt: updatedAt,
		BalanceAsOf: toTimePtr(balanceAsOf), Balance: balance,
	}
}

// List returns accounts for workspaceID. Archived accounts are hidden unless
// includeArchived is true. Balance is derived (spec §5.2) and includes the
// market value of any investment positions held in the account.
func (s *Service) List(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]Account, error) {
	q := dbq.New(s.pool)
	out := make([]Account, 0)
	if includeArchived {
		rows, err := q.ListAccountsWithBalance(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("query accounts: %w", err)
		}
		for _, r := range rows {
			out = append(out, balanceRowToAccount(
				r.ID, r.WorkspaceID, r.Name, r.Nickname, r.Kind, r.Currency,
				r.Institution, r.AccountGroupID, r.AccountSortOrder,
				r.OpenDate, r.CloseDate, r.OpeningBalance, r.OpeningBalanceDate,
				r.IncludeInNetworth, r.IncludeInSavingsRate, r.ArchivedAt,
				r.CreatedAt, r.UpdatedAt, r.BalanceAsOf, r.Balance,
			))
		}
	} else {
		rows, err := q.ListAccountsWithBalanceActive(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("query accounts: %w", err)
		}
		for _, r := range rows {
			out = append(out, balanceRowToAccount(
				r.ID, r.WorkspaceID, r.Name, r.Nickname, r.Kind, r.Currency,
				r.Institution, r.AccountGroupID, r.AccountSortOrder,
				r.OpenDate, r.CloseDate, r.OpeningBalance, r.OpeningBalanceDate,
				r.IncludeInNetworth, r.IncludeInSavingsRate, r.ArchivedAt,
				r.CreatedAt, r.UpdatedAt, r.BalanceAsOf, r.Balance,
			))
		}
	}
	s.applyPositionValue(ctx, workspaceID, out)
	return out, nil
}

// applyPositionValue folds per-account investment market value (in the
// account's own currency) into Account.Balance for the rows in accs. Errors
// are swallowed: a missing quote should not blank the entire accounts list.
func (s *Service) applyPositionValue(ctx context.Context, workspaceID uuid.UUID, accs []Account) {
	if s.positions == nil || len(accs) == 0 {
		return
	}
	mv, err := s.positions.MarketValueByAccount(ctx, workspaceID)
	if err != nil || len(mv) == 0 {
		return
	}
	for i := range accs {
		add, ok := mv[accs[i].ID]
		if !ok || add.IsZero() {
			continue
		}
		base, err := decimal.NewFromString(accs[i].Balance)
		if err != nil {
			continue
		}
		accs[i].Balance = base.Add(add).String()
	}
}

// Get returns a single account by id, scoped to workspaceID. Balance is derived
// (spec §5.2).
func (s *Service) Get(ctx context.Context, workspaceID, accountID uuid.UUID) (*Account, error) {
	r, err := dbq.New(s.pool).GetAccountWithBalance(ctx, dbq.GetAccountWithBalanceParams{
		WorkspaceID: workspaceID,
		ID:          accountID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account")
		}
		return nil, err
	}
	a := balanceRowToAccount(
		r.ID, r.WorkspaceID, r.Name, r.Nickname, r.Kind, r.Currency,
		r.Institution, r.AccountGroupID, r.AccountSortOrder,
		r.OpenDate, r.CloseDate, r.OpeningBalance, r.OpeningBalanceDate,
		r.IncludeInNetworth, r.IncludeInSavingsRate, r.ArchivedAt,
		r.CreatedAt, r.UpdatedAt, r.BalanceAsOf, r.Balance,
	)
	one := []Account{a}
	s.applyPositionValue(ctx, workspaceID, one)
	return &one[0], nil
}

// Update applies a PATCH to an account. Returns the updated Account.
// Disallowed fields (kind, currency, opening balance) are not reachable
// through PatchInput.
func (s *Service) Update(ctx context.Context, workspaceID, accountID uuid.UUID, raw PatchInput) (*Account, error) {
	in, err := raw.normalize()
	if err != nil {
		return nil, err
	}

	// Build a dynamic SET clause. We intentionally keep this tiny — no reflection.
	sets := make([]string, 0, 8)
	args := make([]any, 0, 10)
	args = append(args, workspaceID, accountID) // $1, $2 used in WHERE

	next := func() string {
		args = append(args, nil) // placeholder; caller overwrites in-place
		return fmt.Sprintf("$%d", len(args))
	}

	if in.Name != nil {
		ph := next()
		args[len(args)-1] = strings.TrimSpace(*in.Name)
		sets = append(sets, "name = "+ph)
	}
	if in.Nickname != nil {
		ph := next()
		if *in.Nickname == "" {
			args[len(args)-1] = nil
		} else {
			args[len(args)-1] = *in.Nickname
		}
		sets = append(sets, "nickname = "+ph)
	}
	if in.Kind != nil {
		ph := next()
		args[len(args)-1] = *in.Kind
		// account_kind is an enum; explicit cast keeps Postgres happy.
		sets = append(sets, "kind = "+ph+"::account_kind")
	}
	if in.Institution != nil {
		ph := next()
		if *in.Institution == "" {
			args[len(args)-1] = nil
		} else {
			args[len(args)-1] = *in.Institution
		}
		sets = append(sets, "institution = "+ph)
	}
	if in.AccountGroupID != nil {
		ph := next()
		if *in.AccountGroupID == nil {
			args[len(args)-1] = nil
		} else {
			args[len(args)-1] = **in.AccountGroupID
		}
		sets = append(sets, "account_group_id = "+ph)
	}
	if in.AccountSortOrder != nil {
		ph := next()
		args[len(args)-1] = *in.AccountSortOrder
		sets = append(sets, "account_sort_order = "+ph)
	}
	if in.IncludeInNetworth != nil {
		ph := next()
		args[len(args)-1] = *in.IncludeInNetworth
		sets = append(sets, "include_in_networth = "+ph)
	}
	if in.IncludeInSavingsRate != nil {
		ph := next()
		args[len(args)-1] = *in.IncludeInSavingsRate
		sets = append(sets, "include_in_savings_rate = "+ph)
	}
	if in.CloseDate != nil {
		ph := next()
		if *in.CloseDate == "" {
			args[len(args)-1] = nil
		} else {
			t, _ := time.Parse("2006-01-02", *in.CloseDate) // normalize already validated
			args[len(args)-1] = t
		}
		sets = append(sets, "close_date = "+ph)
	}
	if in.Archived != nil {
		ph := next()
		if *in.Archived {
			args[len(args)-1] = s.now().UTC()
		} else {
			args[len(args)-1] = nil
		}
		sets = append(sets, "archived_at = "+ph)
	}

	if len(sets) == 0 {
		// Nothing to update: behave like a plain GET.
		return s.Get(ctx, workspaceID, accountID)
	}

	query := fmt.Sprintf(`
		update accounts set %s
		where workspace_id = $1 and id = $2
		returning id
	`, strings.Join(sets, ", "))

	var gotID uuid.UUID
	err = s.pool.QueryRow(ctx, query, args...).Scan(&gotID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account")
		}
		return nil, fmt.Errorf("update account: %w", err)
	}
	return s.Get(ctx, workspaceID, accountID)
}

// Delete hard-deletes an account. Use Update(Archived=true) for soft
// removal that keeps historical data; this is the path for genuine "remove
// this entirely" (e.g. cleaning up a buggy import).
//
// `source_refs` is polymorphic and intentionally has no FK to `transactions`
// (it can point at any entity_type), so the transactions FK cascade does
// NOT clean them up. We delete them by hand in the same transaction; if
// that step is skipped, the unique index `source_refs_dedupe_idx` later
// rejects re-imports of the same logical rows with a 23505 collision
// against orphaned source_refs.
func (s *Service) Delete(ctx context.Context, workspaceID, accountID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete account: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbq.New(tx)
	if err := q.DeleteAccountSourceRefs(ctx, dbq.DeleteAccountSourceRefsParams{
		WorkspaceID: workspaceID,
		AccountID:   accountID,
	}); err != nil {
		return fmt.Errorf("delete account source_refs: %w", err)
	}

	n, err := q.DeleteAccount(ctx, dbq.DeleteAccountParams{
		WorkspaceID: workspaceID,
		ID:          accountID,
	})
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if n == 0 {
		return httpx.NewNotFoundError("account")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete account: %w", err)
	}
	return nil
}

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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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
	WorkspaceID             uuid.UUID  `json:"workspaceId"`
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

// Service is the accounts service.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService returns a Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

type AccountGroup struct {
	ID         uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"workspaceId"`
	Name       string     `json:"name"`
	SortOrder  int        `json:"sortOrder"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type CreateGroupInput struct {
	Name string
}

type PatchGroupInput struct {
	Name      *string
	SortOrder *int
	Archived  *bool
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

func scanAccountGroup(row interface{ Scan(...any) error }, g *AccountGroup) error {
	return row.Scan(
		&g.ID, &g.WorkspaceID, &g.Name, &g.SortOrder, &g.ArchivedAt,
		&g.CreatedAt, &g.UpdatedAt,
	)
}

func (s *Service) ListGroups(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]AccountGroup, error) {
	q := `
		select id, workspace_id, name, sort_order, archived_at, created_at, updated_at
		from account_groups
		where workspace_id = $1
	`
	if !includeArchived {
		q += ` and archived_at is null`
	}
	q += ` order by sort_order, created_at`

	rows, err := s.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query account groups: %w", err)
	}
	defer rows.Close()

	out := make([]AccountGroup, 0)
	for rows.Next() {
		var g AccountGroup
		if err := scanAccountGroup(rows, &g); err != nil {
			return nil, fmt.Errorf("scan account group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Service) CreateGroup(ctx context.Context, workspaceID uuid.UUID, raw CreateGroupInput) (*AccountGroup, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		return nil, httpx.NewValidationError("name is required")
	}

	groupID := uuidx.New()
	var g AccountGroup
	err := s.pool.QueryRow(ctx, `
		insert into account_groups (id, workspace_id, name, sort_order)
		values (
			$1, $2, $3,
			coalesce((select max(sort_order) + 1000 from account_groups where workspace_id = $2), 1000)
		)
		returning id, workspace_id, name, sort_order, archived_at, created_at, updated_at
	`, groupID, workspaceID, name).Scan(
		&g.ID, &g.WorkspaceID, &g.Name, &g.SortOrder, &g.ArchivedAt, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, httpx.NewValidationError("account group name already exists")
		}
		return nil, fmt.Errorf("insert account group: %w", err)
	}
	return &g, nil
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
		returning id, workspace_id, name, sort_order, archived_at, created_at, updated_at
	`, strings.Join(sets, ", ")), args...).Scan(
		&g.ID, &g.WorkspaceID, &g.Name, &g.SortOrder, &g.ArchivedAt, &g.CreatedAt, &g.UpdatedAt,
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
	var g AccountGroup
	err := scanAccountGroup(s.pool.QueryRow(ctx, `
		select id, workspace_id, name, sort_order, archived_at, created_at, updated_at
		from account_groups
		where workspace_id = $1 and id = $2
	`, workspaceID, groupID), &g)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account group")
		}
		return nil, err
	}
	return &g, nil
}

func (s *Service) DeleteGroup(ctx context.Context, workspaceID, groupID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete account group: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		update accounts
		set account_group_id = null
		where workspace_id = $1 and account_group_id = $2
	`, workspaceID, groupID); err != nil {
		return fmt.Errorf("clear account group membership: %w", err)
	}
	tag, err := tx.Exec(ctx, `delete from account_groups where workspace_id = $1 and id = $2`, workspaceID, groupID)
	if err != nil {
		return fmt.Errorf("delete account group: %w", err)
	}
	if tag.RowsAffected() == 0 {
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

	for _, group := range in.Groups {
		tag, err := tx.Exec(ctx, `
			update account_groups
			set sort_order = $3
			where workspace_id = $1 and id = $2
		`, workspaceID, group.ID, group.SortOrder)
		if err != nil {
			return fmt.Errorf("reorder account group: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return httpx.NewNotFoundError("account group")
		}
	}

	for _, account := range in.Accounts {
		tag, err := tx.Exec(ctx, `
			update accounts
			set account_group_id = $3, account_sort_order = $4
			where workspace_id = $1 and id = $2
		`, workspaceID, account.ID, account.AccountGroupID, account.SortOrder)
		if err != nil {
			return fmt.Errorf("reorder account: %w", err)
		}
		if tag.RowsAffected() == 0 {
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

	var acc Account
	var balance string
	err = tx.QueryRow(ctx, `
		insert into accounts (
			id, workspace_id, name, nickname, kind, currency, institution,
			account_group_id, account_sort_order,
			open_date, opening_balance, opening_balance_date,
			include_in_networth, include_in_savings_rate
		) values (
			$1, $2, $3, $4, $5::account_kind, $6, $7,
			$8,
			coalesce((
				select max(account_sort_order) + 1000
				from accounts
				where workspace_id = $2
				  and account_group_id is not distinct from $8::uuid
			), 1000),
			$9, $10::numeric, $11,
			$12, $13
		)
		returning id, workspace_id, name, nickname, kind::text, currency, institution,
		          account_group_id, account_sort_order,
		          open_date, close_date, opening_balance::text, opening_balance_date,
		          include_in_networth, include_in_savings_rate, archived_at,
		          created_at, updated_at
	`, accountID, workspaceID, in.Name, in.Nickname, in.Kind, in.Currency, in.Institution,
		in.AccountGroupID, in.OpenDate, openingBalanceStr, *in.OpeningBalanceDate,
		*in.IncludeInNetworth, *in.IncludeInSavingsRate).
		Scan(&acc.ID, &acc.WorkspaceID, &acc.Name, &acc.Nickname, &acc.Kind, &acc.Currency, &acc.Institution,
			&acc.AccountGroupID, &acc.AccountSortOrder,
			&acc.OpenDate, &acc.CloseDate, &balance, &acc.OpeningBalanceDate,
			&acc.IncludeInNetworth, &acc.IncludeInSavingsRate, &acc.ArchivedAt,
			&acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}
	acc.OpeningBalance = balance

	_, err = tx.Exec(ctx, `
		insert into account_balance_snapshots (
			id, workspace_id, account_id, as_of, balance, currency, source
		) values (
			$1, $2, $3, $4, $5::numeric, $6, 'opening'
		)
	`, snapshotID, workspaceID, accountID, openingTS, openingBalanceStr, in.Currency)
	if err != nil {
		return nil, fmt.Errorf("insert opening snapshot: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	acc.Balance = openingBalanceStr
	ts := openingTS
	acc.BalanceAsOf = &ts
	return &acc, nil
}

// Derived balance rule (spec §5.2, implemented here):
//
//	balance = coalesce(latest_snapshot.balance, opening_balance)
//	        + sum(transactions.amount for this account
//	              where status in ('posted','reconciled')
//	                and booked_at >= (latest_snapshot.as_of at time zone 'UTC')::date)
//
// Drafts and voided transactions are excluded. The snapshot's as_of
// (timestamptz) is projected into UTC and cast to date so we can compare it
// against transactions.booked_at (a date). Every account receives an
// "opening" snapshot at midnight UTC of its opening_balance_date on create,
// so post-opening-day transactions are included correctly. When richer
// snapshot kinds (end-of-day recomputes, bank syncs) arrive this rule will
// need revisiting — an end-of-day snapshot would require `>` not `>=`.
const derivedBalanceSelect = `
	a.id, a.workspace_id, a.name, a.nickname, a.kind::text, a.currency, a.institution,
	a.account_group_id, a.account_sort_order,
	a.open_date, a.close_date, a.opening_balance::text, a.opening_balance_date,
	a.include_in_networth, a.include_in_savings_rate, a.archived_at,
	a.created_at, a.updated_at,
	case
	  when t.max_booked_at is null then s.as_of
	  when s.as_of is null then t.max_booked_at::timestamp at time zone 'UTC'
	  when t.max_booked_at::timestamp at time zone 'UTC' > s.as_of then t.max_booked_at::timestamp at time zone 'UTC'
	  else s.as_of
	end as balance_as_of,
	(coalesce(s.balance, a.opening_balance) + coalesce(t.post_sum, 0))::text as balance
`

const derivedBalanceFrom = `
	from accounts a
	left join lateral (
	  select balance, as_of
	  from account_balance_snapshots
	  where account_id = a.id
	  order by as_of desc
	  limit 1
	) s on true
	left join lateral (
	  select coalesce(sum(amount), 0) as post_sum, max(booked_at) as max_booked_at
	  from transactions
	  where account_id = a.id
	    and status in ('posted', 'reconciled')
	    and booked_at >= (coalesce(s.as_of, a.opening_balance_date::timestamptz)
	                        at time zone 'UTC')::date
	) t on true
`

func scanAccount(row interface{ Scan(...any) error }, a *Account) error {
	var asOf *time.Time
	var balance string
	if err := row.Scan(
		&a.ID, &a.WorkspaceID, &a.Name, &a.Nickname, &a.Kind, &a.Currency, &a.Institution,
		&a.AccountGroupID, &a.AccountSortOrder,
		&a.OpenDate, &a.CloseDate, &a.OpeningBalance, &a.OpeningBalanceDate,
		&a.IncludeInNetworth, &a.IncludeInSavingsRate, &a.ArchivedAt,
		&a.CreatedAt, &a.UpdatedAt, &asOf, &balance,
	); err != nil {
		return err
	}
	a.Balance = balance
	a.BalanceAsOf = asOf
	return nil
}

// List returns accounts for workspaceID. Archived accounts are hidden unless
// includeArchived is true. Balance is derived (see derivedBalanceSelect).
func (s *Service) List(ctx context.Context, workspaceID uuid.UUID, includeArchived bool) ([]Account, error) {
	q := `select ` + derivedBalanceSelect + derivedBalanceFrom + ` where a.workspace_id = $1`
	if !includeArchived {
		q += ` and a.archived_at is null`
	}
	q += ` order by a.account_group_id nulls first, a.account_sort_order, a.created_at`

	rows, err := s.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()

	out := make([]Account, 0)
	for rows.Next() {
		var a Account
		if err := scanAccount(rows, &a); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns a single account by id, scoped to workspaceID. Balance is derived
// (see derivedBalanceSelect).
func (s *Service) Get(ctx context.Context, workspaceID, accountID uuid.UUID) (*Account, error) {
	var a Account
	row := s.pool.QueryRow(ctx,
		`select `+derivedBalanceSelect+derivedBalanceFrom+
			` where a.workspace_id = $1 and a.id = $2`,
		workspaceID, accountID)
	if err := scanAccount(row, &a); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NewNotFoundError("account")
		}
		return nil, err
	}
	return &a, nil
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

	if _, err := tx.Exec(ctx, `
		delete from source_refs
		where workspace_id = $1
		  and entity_type = 'transaction'
		  and entity_id in (
		    select id from transactions where workspace_id = $1 and account_id = $2
		  )
	`, workspaceID, accountID); err != nil {
		return fmt.Errorf("delete account source_refs: %w", err)
	}

	tag, err := tx.Exec(ctx, `delete from accounts where workspace_id = $1 and id = $2`, workspaceID, accountID)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NewNotFoundError("account")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete account: %w", err)
	}
	return nil
}

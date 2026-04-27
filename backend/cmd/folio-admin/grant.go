package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/xmedavid/folio/backend/internal/admin"
	"github.com/xmedavid/folio/backend/internal/db/dbq"
)

func newGrantCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "grant <email>",
		Short: "Promote a user to admin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			pool, err := openPool(ctx)
			if err != nil {
				return err
			}
			defer pool.Close()

			email := strings.ToLower(strings.TrimSpace(args[0]))
			userID, err := dbq.New(pool).GetUserIDByEmail(ctx, email)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("user not found")
			}
			if err != nil {
				return err
			}
			if err := admin.NewService(pool).GrantAdmin(ctx, userID, uuid.Nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "granted: %s\n", email)
			return nil
		},
	}
}

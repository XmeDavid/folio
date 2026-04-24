package main

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all admins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			pool, err := openPool(ctx)
			if err != nil {
				return err
			}
			defer pool.Close()

			rows, err := pool.Query(ctx, `select email::text, id, updated_at from users where is_admin = true order by email`)
			if err != nil {
				return err
			}
			defer rows.Close()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "EMAIL\tUSER_ID\tUPDATED_AT")
			for rows.Next() {
				var email string
				var id uuid.UUID
				var at time.Time
				if err := rows.Scan(&email, &id, &at); err != nil {
					return err
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", email, id, at.Format(time.RFC3339))
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return w.Flush()
		},
	}
}

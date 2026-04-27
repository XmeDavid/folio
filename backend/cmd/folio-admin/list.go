package main

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/xmedavid/folio/backend/internal/db/dbq"
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

			admins, err := dbq.New(pool).ListAdminUsers(ctx)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "EMAIL\tUSER_ID\tUPDATED_AT")
			for _, a := range admins {
				fmt.Fprintf(w, "%s\t%s\t%s\n", a.Email, a.ID, a.UpdatedAt.Format(time.RFC3339))
			}
			return w.Flush()
		},
	}
}

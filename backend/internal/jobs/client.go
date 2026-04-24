package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

type Client struct {
	inner *river.Client[pgx.Tx]
}

type Config struct {
	Queues map[string]river.QueueConfig
}

func NewClient(pool *pgxpool.Pool, workers *river.Workers, cfg Config) (*Client, error) {
	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  cfg.Queues,
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Client{inner: rc}, nil
}

func (c *Client) Start(ctx context.Context) error { return c.inner.Start(ctx) }

func (c *Client) Stop(ctx context.Context) error { return c.inner.Stop(ctx) }

func (c *Client) Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return c.inner.Insert(ctx, args, opts)
}

func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return c.inner.InsertTx(ctx, tx, args, opts)
}

func (c *Client) JobRetry(ctx context.Context, id int64) (*rivertype.JobRow, error) {
	return c.inner.JobRetry(ctx, id)
}

func (c *Client) Inner() *river.Client[pgx.Tx] { return c.inner }

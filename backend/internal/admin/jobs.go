package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/xmedavid/folio/backend/internal/httpx"
)

type jobCursor struct {
	ScheduledAt time.Time `json:"scheduledAt"`
	ID          int64     `json:"id"`
}

type Job struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	Queue       string          `json:"queue"`
	State       string          `json:"state"`
	AttemptedAt *time.Time      `json:"attemptedAt,omitempty"`
	ScheduledAt time.Time       `json:"scheduledAt"`
	Errors      json.RawMessage `json:"errors,omitempty"`
	Args        json.RawMessage `json:"args"`
}

func (s *Service) ListJobs(ctx context.Context, filter JobFilter) ([]Job, Pagination, error) {
	filter.AdminListFilter = filter.Normalize()
	var cur jobCursor
	if filter.Cursor != "" {
		if err := decodeCursor(filter.Cursor, &cur); err != nil {
			return nil, Pagination{}, httpx.NewValidationError("invalid cursor")
		}
	}
	rows, err := s.pool.Query(ctx, `
		select id, kind, queue, state, attempted_at, scheduled_at, errors, args
		from river_job
		where ($1::text = '' or state = $1)
		  and ($2::text = '' or kind = $2)
		  and ($3::timestamptz is null or (scheduled_at, id) < ($3, $4))
		order by scheduled_at desc, id desc
		limit $5
	`, filter.State, filter.Kind, nullTime(cur.ScheduledAt), nullInt64(cur.ID), filter.Limit+1)
	if err != nil {
		return nil, Pagination{}, err
	}
	defer rows.Close()
	jobs := make([]Job, 0, filter.Limit)
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Kind, &j.Queue, &j.State, &j.AttemptedAt, &j.ScheduledAt, &j.Errors, &j.Args); err != nil {
			return nil, Pagination{}, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, Pagination{}, err
	}
	return pageJobs(jobs, filter.Limit)
}

func pageJobs(rows []Job, limit int) ([]Job, Pagination, error) {
	p := Pagination{Limit: limit}
	if len(rows) <= limit {
		return rows, p, nil
	}
	spill := rows[limit]
	c, err := encodeCursor(jobCursor{ScheduledAt: spill.ScheduledAt, ID: spill.ID})
	if err != nil {
		return nil, Pagination{}, err
	}
	p.NextCursor = &c
	return rows[:limit], p, nil
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

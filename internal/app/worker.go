package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/capture"
	"llm-gateway/internal/config"
	"llm-gateway/internal/store"
)

type Worker struct {
	Store   *store.Store
	Archive capture.Archive
	Config  config.Config
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if e := w.Tick(ctx); e != nil && !errors.Is(e, context.Canceled) {
			slog.Error("archive worker tick failed", "error", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *Worker) Tick(ctx context.Context) error {
	// Crashed requests are finalized conservatively; unknown usage consumes the reservation.
	rows, e := w.Store.DB.Query(ctx, `SELECT id FROM requests WHERE finished_at IS NULL AND last_activity_at<now()-make_interval(secs=>$1)`, w.Config.RequestTimeout.Seconds()+60)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = w.Store.Finish(ctx, store.Request{ID: id, State: "interrupted", Error: "process_interrupted", UsageStatus: "unknown"}); e != nil {
			return e
		}
		if _, e = w.Store.DB.Exec(ctx, `UPDATE request_attempts SET state='interrupted',finished_at=now() WHERE request_id=$1 AND finished_at IS NULL`, id); e != nil {
			return e
		}
	}
	for i := 0; i < 25; i++ {
		ok, e := w.archiveOne(ctx)
		if e != nil {
			return e
		}
		if !ok {
			break
		}
	}
	for i := 0; i < 25; i++ {
		ok, e := w.expireOne(ctx)
		if e != nil {
			return e
		}
		if !ok {
			break
		}
	}
	_, e = w.Store.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at<now()`)
	return e
}
func (w *Worker) archiveOne(ctx context.Context) (bool, error) {
	tx, e := w.Store.DB.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var id string
	e = tx.QueryRow(ctx, `SELECT id FROM requests WHERE finished_at IS NOT NULL AND archive_key IS NULL AND finished_at<now()-make_interval(secs=>$1) AND expires_at>now() ORDER BY finished_at FOR UPDATE SKIP LOCKED LIMIT 1`, w.Config.Capture.ArchiveAfter.Seconds()).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	events, e := store.Events(ctx, tx, id)
	if e != nil {
		return false, e
	}
	b, e := json.Marshal(events)
	if e != nil {
		return false, e
	}
	key := id + ".json.enc"
	if e = w.Archive.Put(ctx, key, b); e != nil {
		return false, e
	}
	if _, e = tx.Exec(ctx, `UPDATE requests SET archive_key=$2 WHERE id=$1`, id, key); e != nil {
		return false, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM capture_events WHERE request_id=$1`, id); e != nil {
		return false, e
	}
	return true, tx.Commit(ctx)
}
func (w *Worker) expireOne(ctx context.Context) (bool, error) {
	tx, e := w.Store.DB.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	var id string
	var key *string
	e = tx.QueryRow(ctx, `SELECT id,archive_key FROM requests WHERE finished_at IS NOT NULL AND expires_at<now() FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &key)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if key != nil {
		if e = w.Archive.Delete(ctx, *key); e != nil {
			return false, e
		}
	}
	if _, e = tx.Exec(ctx, `DELETE FROM requests WHERE id=$1`, id); e != nil {
		return false, e
	}
	return true, tx.Commit(ctx)
}

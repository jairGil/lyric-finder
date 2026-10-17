package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"lyrics/internal/domain"
)

func (store *Store) CreateRenderJob(ctx context.Context, id string, userID int64, artist, title, mediaName string, spec domain.RenderSpecRecord) error {
	payload, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO render_jobs (id, user_id, artist, title, status, media_name, spec_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, nullableUserID(userID), artist, title, string(domain.RenderQueued), mediaName, payload)
	return err
}

func (store *Store) MarkRenderRunning(ctx context.Context, id string) error {
	return store.setRenderState(ctx, id, string(domain.RenderRunning), 0, "", "")
}

func (store *Store) MarkRenderProgress(ctx context.Context, id string, progress float64) {
	_, _ = store.db.ExecContext(ctx, `
		UPDATE render_jobs SET progress = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ?`,
		progress, id, string(domain.RenderRunning))
}

func (store *Store) MarkRenderDone(ctx context.Context, id, outputName string) error {
	return store.setRenderState(ctx, id, string(domain.RenderDone), 1, "", outputName)
}

func (store *Store) MarkRenderFailed(ctx context.Context, id, message string) error {
	return store.setRenderState(ctx, id, string(domain.RenderFailed), 0, message, "")
}

func (store *Store) setRenderState(ctx context.Context, id, status string, progress float64, message, outputName string) error {
	_, err := store.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET status = ?, progress = ?, error = ?, output_name = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		status, progress, message, outputName, id)
	return err
}

func (store *Store) GetRenderJob(ctx context.Context, id string) (domain.RenderJob, domain.RenderSpecRecord, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT id, artist, title, status, progress, media_name, output_name, error, spec_json, created_at, updated_at
		FROM render_jobs WHERE id = ?`, id)

	var (
		job     domain.RenderJob
		status  string
		rawSpec []byte
		created string
		updated string
		spec    domain.RenderSpecRecord
	)

	err := row.Scan(
		&job.ID, &job.Artist, &job.Title, &status, &job.Progress,
		&job.MediaName, &job.OutputName, &job.Error, &rawSpec, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RenderJob{}, domain.RenderSpecRecord{}, ErrNotFound
	}
	if err != nil {
		return domain.RenderJob{}, domain.RenderSpecRecord{}, err
	}

	job.Status = domain.RenderStatus(status)
	job.CreatedAt = parseTimestamp(created)
	job.UpdatedAt = parseTimestamp(updated)

	if len(rawSpec) > 0 {
		if err := json.Unmarshal(rawSpec, &spec); err != nil {
			return domain.RenderJob{}, domain.RenderSpecRecord{}, err
		}
	}
	return job, spec, nil
}

func (store *Store) ResetStaleRenderJobs(ctx context.Context) (int64, error) {
	result, err := store.db.ExecContext(ctx, `
		UPDATE render_jobs
		SET status = ?, error = 'el servidor se reinició durante el render', updated_at = CURRENT_TIMESTAMP
		WHERE status IN (?, ?)`,
		string(domain.RenderFailed), string(domain.RenderQueued), string(domain.RenderRunning))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func nullableUserID(userID int64) any {
	if userID <= 0 {
		return nil
	}
	return userID
}

func parseTimestamp(value string) time.Time {
	for _, layout := range []string{time.DateTime, time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

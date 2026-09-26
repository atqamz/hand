package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	ReportProgress = "progress"
	ReportDone     = "done"
	ReportStuck    = "stuck"
)

const MaxReportBytes = 65536

var reportStatuses = []string{ReportProgress, ReportDone, ReportStuck}

type Report struct {
	ID        int64
	AttemptID int64
	TaskID    int64
	Status    string
	Body      string
	CreatedAt string
	AckedAt   string
	AckedBy   string
}

func (r Report) Summary() string {
	for _, l := range strings.Split(r.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

type ReportFilter struct {
	AttemptID int64
	TaskID    int64
	Unacked   bool
}

const reportSelect = `SELECT id, attempt_id, task_id, status, body, created_at, acked_at, acked_by FROM report`

func scanReport(row scanner) (Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.AttemptID, &r.TaskID, &r.Status, &r.Body, &r.CreatedAt, &r.AckedAt, &r.AckedBy)
	return r, err
}

func (f ReportFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.AttemptID != 0 {
		conds, args = append(conds, "attempt_id = ?"), append(args, f.AttemptID)
	}
	if f.TaskID != 0 {
		conds, args = append(conds, "task_id = ?"), append(args, f.TaskID)
	}
	if f.Unacked {
		conds = append(conds, "acked_at = ''")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Store) AddReport(ctx context.Context, attemptID int64, status, body string) (Report, error) {
	if !slices.Contains(reportStatuses, status) {
		return Report{}, fmt.Errorf("%w: report status %q must be progress, done or stuck", ErrInvalid, status)
	}
	if strings.TrimSpace(body) == "" || len(body) > MaxReportBytes {
		return Report{}, fmt.Errorf("%w: report body must be 1-%d bytes", ErrInvalid, MaxReportBytes)
	}
	r := Report{AttemptID: attemptID, Status: status, Body: body, CreatedAt: s.stamp()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := getAttempt(tx, attemptID)
		if err != nil {
			return err
		}
		if a.Status != AttemptRunning {
			return fmt.Errorf("%w: attempt %s is %s; only a running attempt can report", ErrConflict, AttemptRef(attemptID), a.Status)
		}
		r.TaskID = a.TaskID
		res, err := tx.Exec(`INSERT INTO report(attempt_id, task_id, status, body, created_at) VALUES (?, ?, ?, ?, ?)`,
			r.AttemptID, r.TaskID, r.Status, r.Body, r.CreatedAt)
		if err != nil {
			return err
		}
		if r.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return emit(tx, r.CreatedAt, "attempt.reported", r.TaskID, AttemptRef(attemptID)+": "+ReportRef(r.ID)+" "+status)
	})
	return r, err
}

func (s *Store) AckReport(ctx context.Context, id int64, by string) (Report, error) {
	if strings.TrimSpace(by) == "" || strings.ContainsAny(by, "\r\n") {
		return Report{}, fmt.Errorf("%w: acknowledged-by must be one non-empty line", ErrInvalid)
	}
	var out Report
	err := s.tx(ctx, func(tx *sql.Tx) error {
		r, err := scanReport(tx.QueryRow(reportSelect+` WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: report %s", ErrNotFound, ReportRef(id))
		}
		if err != nil {
			return err
		}
		if r.AckedAt != "" {
			return fmt.Errorf("%w: report %s is already acknowledged", ErrConflict, ReportRef(id))
		}
		now := s.stamp()
		if _, err := tx.Exec(`UPDATE report SET acked_at = ?, acked_by = ? WHERE id = ?`, now, by, id); err != nil {
			return err
		}
		r.AckedAt, r.AckedBy = now, by
		out = r
		return emit(tx, now, "report.acked", r.TaskID, ReportRef(id)+" by "+by)
	})
	return out, err
}

func (s *Store) Report(ctx context.Context, id int64) (Report, error) {
	r, err := scanReport(s.db.QueryRowContext(ctx, reportSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, fmt.Errorf("%w: report %s", ErrNotFound, ReportRef(id))
	}
	return r, err
}

func (s *Store) Reports(ctx context.Context, f ReportFilter, limit int) ([]Report, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: limit must be at least 1", ErrInvalid)
	}
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (`+reportSelect+where+` ORDER BY id DESC LIMIT ?) ORDER BY id`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) LatestReport(ctx context.Context, f ReportFilter) (Report, bool, error) {
	where, args := f.where()
	r, err := scanReport(s.db.QueryRowContext(ctx, reportSelect+where+` ORDER BY id DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, false, nil
	}
	return r, err == nil, err
}

func (s *Store) UnackedReportCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM report WHERE acked_at = ''`).Scan(&n)
	return n, err
}

func (s *Store) ReportedSinceQuiet(ctx context.Context, attemptID int64) (bool, error) {
	prefix := AttemptRef(attemptID) + ": %"
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM event WHERE kind = 'attempt.reported' AND detail LIKE ?
		AND seq > COALESCE((SELECT MAX(seq) FROM event WHERE kind = 'attempt.quiet' AND detail LIKE ?), 0)
	)`, prefix, prefix).Scan(&ok)
	return ok, err
}

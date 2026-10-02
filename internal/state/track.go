package state

import (
	"context"
	"slices"
	"time"
)

type Track struct {
	Harness, Model, Effort string
	N, FirstTry, Reattempt int
	Stuck, Sent, Wakes     int
	Median                 float64
	Timed                  bool
}

const trackQuery = `SELECT a.harness, a.model, a.effort, a.created_at,
	a.id = last.id AND t.status = 'done',
	a.id < last.id,
	EXISTS (SELECT 1 FROM report WHERE attempt_id = a.id AND status = 'stuck'),
	(SELECT count(*) FROM event WHERE kind = 'attempt.sent' AND detail LIKE 'a' || a.id || ': %'),
	(SELECT count(*) FROM event WHERE kind IN ('attempt.quiet', 'attempt.blocked') AND detail LIKE 'a' || a.id || ': %'),
	COALESCE((SELECT min(created_at) FROM report WHERE attempt_id = a.id AND status = 'done'), '')
FROM attempt a
JOIN task t ON t.id = a.task_id
JOIN (SELECT task_id, max(id) AS id FROM attempt GROUP BY task_id) last ON last.task_id = a.task_id
WHERE a.ended_at != ''
ORDER BY a.harness, a.model, a.effort, a.id`

func (s *Store) TrackRecord(ctx context.Context) ([]Track, error) {
	rows, err := s.db.QueryContext(ctx, trackQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Track
	var mins [][]float64
	for rows.Next() {
		var h, m, e, created, doneAt string
		var first, later, stuck bool
		var sent, wakes int
		if err := rows.Scan(&h, &m, &e, &created, &first, &later, &stuck, &sent, &wakes, &doneAt); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Harness != h || out[len(out)-1].Model != m || out[len(out)-1].Effort != e {
			out = append(out, Track{Harness: h, Model: m, Effort: e})
			mins = append(mins, nil)
		}
		i := len(out) - 1
		out[i].N++
		out[i].Sent += sent
		out[i].Wakes += wakes
		for _, c := range []struct {
			on bool
			to *int
		}{{first, &out[i].FirstTry}, {later, &out[i].Reattempt}, {stuck, &out[i].Stuck}} {
			if c.on {
				*c.to++
			}
		}
		if doneAt == "" {
			continue
		}
		from, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		to, err := time.Parse(time.RFC3339Nano, doneAt)
		if err != nil {
			return nil, err
		}
		mins[i] = append(mins[i], to.Sub(from).Minutes())
	}
	for i, d := range mins {
		if len(d) == 0 {
			continue
		}
		slices.Sort(d)
		out[i].Timed = true
		out[i].Median = d[len(d)/2]
		if len(d)%2 == 0 {
			out[i].Median = (d[len(d)/2-1] + d[len(d)/2]) / 2
		}
	}
	return out, rows.Err()
}

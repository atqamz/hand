package state

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/redact"
)

type Reply struct {
	At   string
	N    int
	Body string
}

const MaxReplyBody = 32768

func capReply(s string) string {
	if len(s) <= MaxReplyBody {
		return s
	}
	keep := MaxReplyBody - 48
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + "[… " + strconv.Itoa(len(s)-keep) + " bytes cut …]"
}

func (s *Store) AddSupervisorReplies(ctx context.Context, supervisorID int64, session string, replies []Reply) (int, error) {
	if session == "" {
		return 0, fmt.Errorf("%w: a reply needs the session it came from", ErrInvalid)
	}
	bodies := make([]string, len(replies))
	for i, r := range replies {
		if r.N < 0 {
			return 0, fmt.Errorf("%w: reply number %d is negative", ErrInvalid, r.N)
		}
		bodies[i] = capReply(redact.Text(r.Body))
	}
	inserted := 0
	err := s.tx(ctx, func(tx *sql.Tx) error {
		inserted = 0
		var first, last int64
		for i, r := range replies {
			if bodies[i] == "" {
				continue
			}
			res, err := tx.Exec(`INSERT INTO supervisor_reply(supervisor_id, session, at, n, body) VALUES (?, ?, ?, ?, ?) ON CONFLICT(session, at, n) DO NOTHING`, supervisorID, session, r.At, r.N, bodies[i])
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				continue
			}
			if last, err = res.LastInsertId(); err != nil {
				return err
			}
			if inserted == 0 {
				first = last
			}
			inserted++
		}
		if inserted == 0 {
			return nil
		}
		detail := SupervisorRef(supervisorID) + ": " + ReplyRef(first)
		if inserted > 1 {
			detail += ".." + ReplyRef(last) + " (" + strconv.Itoa(inserted) + ")"
		}
		return emit(tx, s.stamp(), "supervisor.replied", 0, detail)
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

func (s *Store) NewReplies(ctx context.Context, session string, replies []Reply) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, n FROM supervisor_reply WHERE session = ?`, session)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type key struct {
		at string
		n  int
	}
	known := map[key]bool{}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.at, &k.n); err != nil {
			return 0, err
		}
		known[k] = true
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	fresh := 0
	for _, r := range replies {
		if !known[key{r.At, r.N}] {
			fresh++
		}
	}
	return fresh, nil
}

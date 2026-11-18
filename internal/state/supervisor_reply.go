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
	inserted := 0
	err := s.tx(ctx, func(tx *sql.Tx) error {
		inserted = 0
		var first, last int64
		for _, r := range replies {
			if r.N < 0 {
				return fmt.Errorf("%w: reply number %d is negative", ErrInvalid, r.N)
			}
			body := capReply(redact.Text(r.Body))
			if body == "" {
				continue
			}
			res, err := tx.Exec(`INSERT INTO supervisor_reply(supervisor_id, session, at, n, body) VALUES (?, ?, ?, ?, ?) ON CONFLICT(session, at, n) DO NOTHING`, supervisorID, session, r.At, r.N, body)
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

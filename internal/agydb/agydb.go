package agydb

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Step struct {
	Type int
	Text string
	At   time.Time
	Tool bool
}

var uriPath = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

func Open(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+uriPath.Replace(path)+"?mode=ro&_pragma=busy_timeout(100)")
}

func Steps(db *sql.DB) ([]Step, error) {
	rows, err := db.Query("SELECT step_type, step_payload FROM steps WHERE step_type IN (14, 15) ORDER BY idx")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var typ int
		var p []byte
		if err := rows.Scan(&typ, &p); err != nil {
			return nil, err
		}
		if _, ok := fields(p); !ok {
			continue
		}
		s := Step{Type: typ, At: stamp(p)}
		switch typ {
		case 14:
			text, _ := path(p, 19, 2)
			s.Text = string(text.bytes)
		case 15:
			text, _ := path(p, 20, 1)
			_, tool := path(p, 20, 7)
			s.Text, s.Tool = string(text.bytes), tool
		default:
			continue
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func stamp(p []byte) time.Time {
	sec, ok := path(p, 5, 1, 1)
	if !ok {
		return time.Time{}
	}
	nanos, _ := path(p, 5, 1, 2)
	return time.Unix(int64(sec.varint), int64(nanos.varint)).UTC()
}

func Workspace(db *sql.DB) (string, error) {
	rows, err := db.Query("SELECT step_payload FROM steps WHERE step_type = 14 ORDER BY idx")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			return "", err
		}
		if ws, ok := path(p, 19, 12, 1, 42, 11, 1); ok && len(ws.bytes) > 0 {
			return string(ws.bytes), nil
		}
	}
	return "", rows.Err()
}

func Usage(db *sql.DB) (used, window int64, err error) {
	var data []byte
	err = db.QueryRow("SELECT data FROM gen_metadata ORDER BY idx DESC LIMIT 1").Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	u, _ := path(data, 1, 9, 10, 1)
	w, _ := path(data, 1, 9, 10, 4)
	return int64(u.varint), int64(w.varint), nil
}

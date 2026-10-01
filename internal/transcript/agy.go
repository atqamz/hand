package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/agydb"
)

func (r *Reader) agy(s *session, id string) error {
	if r.Paths.Agy == "" {
		return errors.New("agy conversations folder unknown")
	}
	path := filepath.Join(r.Paths.Agy, id+".db")
	stamp := newest(path, path+"-wal")
	switch {
	case stamp.IsZero():
		return ErrNoSession
	case stamp.Equal(s.stamp):
		return nil
	}
	db, err := agydb.Open(path)
	if err != nil {
		return keep(s)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM steps").Scan(&rows); err != nil {
		return keep(s)
	}
	steps, err := agydb.Steps(db)
	if err != nil {
		return keep(s)
	}
	used, window, err := agydb.Usage(db)
	if err != nil {
		return keep(s)
	}
	fresh := session{offsets: map[string]int64{}, stamp: stamp, lines: rows, known: len(steps)}
	fresh.status.Context, fresh.status.Window = used, window
	for _, st := range steps {
		at := st.At.UTC().Format("2006-01-02T15:04:05.000Z")
		switch st.Type {
		case 14:
			fresh.user(st.Text, at)
		case 15:
			fresh.reply(st.Text, at, !st.Tool)
		}
	}
	*s = fresh
	return nil
}

func keep(s *session) error {
	if s.known > 0 {
		return nil
	}
	return ErrNoSession
}

func newest(paths ...string) time.Time {
	var out time.Time
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(out) {
			out = fi.ModTime()
		}
	}
	return out
}

package agytest

import (
	"database/sql"
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type Step struct {
	Type      int
	Text      string
	At        time.Time
	Tool      bool
	Workspace string
	Raw       []byte
}

func varint(num int, v uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(num)<<3), v)
}

func bytesField(num int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

func nest(b []byte, nums ...int) []byte {
	for i := len(nums) - 1; i >= 0; i-- {
		b = bytesField(nums[i], b)
	}
	return b
}

func stamp(at time.Time) []byte {
	return bytesField(1, append(varint(1, uint64(at.Unix())), varint(2, uint64(at.Nanosecond()))...))
}

func payload(s Step, workspace string) []byte {
	if s.Raw != nil {
		return s.Raw
	}
	b := append(varint(1, uint64(s.Type)), bytesField(5, stamp(s.At))...)
	switch s.Type {
	case 14:
		body := bytesField(2, []byte(s.Text))
		if workspace != "" {
			body = append(body, nest(bytesField(1, []byte(workspace)), 12, 1, 42, 11)...)
		}
		b = append(b, bytesField(19, body)...)
	case 15:
		var body []byte
		if s.Text != "" {
			body = bytesField(1, []byte(s.Text))
		}
		if s.Tool {
			body = append(body, bytesField(7, append(bytesField(1, []byte("call_1")), bytesField(2, []byte("run_command"))...))...)
		}
		b = append(b, bytesField(20, body)...)
	default:
		b = append(b, bytesField(140, bytesField(2, []byte(s.Text)))...)
	}
	return b
}

func usage(used, window int64) []byte {
	return nest(append(varint(1, uint64(used)), varint(4, uint64(window))...), 1, 9, 10)
}

func Conversation(t testing.TB, dir, id, workspace string, used, window int64, steps ...Step) string {
	t.Helper()
	path := filepath.Join(dir, id+".db")
	db := open(t, path)
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE `steps` (`idx` integer,`step_type` integer NOT NULL DEFAULT 0,`status` integer NOT NULL DEFAULT 0,`has_subtrajectory` numeric NOT NULL DEFAULT false,`metadata` blob,`error_details` blob,`permissions` blob,`task_details` blob,`render_info` blob,`step_payload` blob,`step_format` integer NOT NULL DEFAULT 0,PRIMARY KEY (`idx`))",
		"CREATE TABLE `gen_metadata` (`idx` integer,`data` blob,`size` integer NOT NULL DEFAULT 0,PRIMARY KEY (`idx`))",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	insert(t, db, 0, workspace, window, used, steps)
	return path
}

func Append(t testing.TB, path string, used, window int64, steps ...Step) {
	t.Helper()
	db := open(t, path)
	defer db.Close()
	var next int64
	if err := db.QueryRow("SELECT COALESCE(MAX(idx) + 1, 0) FROM steps").Scan(&next); err != nil {
		t.Fatal(err)
	}
	insert(t, db, next, "", window, used, steps)
}

func insert(t testing.TB, db *sql.DB, from int64, workspace string, window, used int64, steps []Step) {
	t.Helper()
	for i, s := range steps {
		ws := s.Workspace
		if from+int64(i) == 0 && ws == "" {
			ws = workspace
		}
		if _, err := db.Exec("INSERT INTO steps (idx, step_type, step_payload) VALUES (?, ?, ?)", from+int64(i), s.Type, payload(s, ws)); err != nil {
			t.Fatal(err)
		}
	}
	var gen int64
	if err := db.QueryRow("SELECT COALESCE(MAX(idx) + 1, 0) FROM gen_metadata").Scan(&gen); err != nil {
		t.Fatal(err)
	}
	data := usage(used, window)
	if _, err := db.Exec("INSERT INTO gen_metadata (idx, data, size) VALUES (?, ?, ?)", gen, data, len(data)); err != nil {
		t.Fatal(err)
	}
}

func open(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

package fakebin

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
)

const magic = "\x00fakebin"

type spec struct {
	Behavior string            `json:"behavior"`
	Params   map[string]string `json:"params"`
}

var params map[string]string

func Main(behaviors map[string]func(args []string) int) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	b, err := os.ReadFile(exe + ".fake")
	if err != nil {
		if b = embedded(exe); b == nil {
			return
		}
	}
	var s spec
	if err := json.Unmarshal(b, &s); err != nil {
		fmt.Fprintln(os.Stderr, "fakebin:", err)
		os.Exit(2)
	}
	run := behaviors[s.Behavior]
	if run == nil {
		fmt.Fprintf(os.Stderr, "fakebin: no behavior %q\n", s.Behavior)
		os.Exit(2)
	}
	params = s.Params
	syscall.Exit(run(os.Args[1:]))
}

func Params() map[string]string { return params }

func Install(t testing.TB, dir, name, behavior string, params map[string]string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	_ = os.Remove(path)
	if os.Link(exe, path) != nil {
		b, err := os.ReadFile(exe)
		if err == nil {
			err = os.WriteFile(path, b, 0o755)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := json.Marshal(spec{behavior, params})
	if err == nil {
		err = os.WriteFile(path+".fake", b, 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
	return path
}

var (
	mu     sync.Mutex
	self   []byte
	embeds = map[string][]byte{}
)

func Embed(t testing.TB, behavior string, params map[string]string) []byte {
	t.Helper()
	b, err := json.Marshal(spec{behavior, params})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if out, ok := embeds[string(b)]; ok {
		return out
	}
	if self == nil {
		exe, err := os.Executable()
		if err == nil {
			self, err = os.ReadFile(exe)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	out := append(binary.BigEndian.AppendUint64(append(self[:len(self):len(self)], b...), uint64(len(b))), magic...)
	embeds[string(b)] = out
	return out
}

func embedded(exe string) []byte {
	f, err := os.Open(exe)
	if err != nil {
		return nil
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	tail := make([]byte, 8+len(magic))
	if err != nil || end < int64(len(tail)) {
		return nil
	}
	if _, err := f.ReadAt(tail, end-int64(len(tail))); err != nil || string(tail[8:]) != magic {
		return nil
	}
	n := int64(binary.BigEndian.Uint64(tail))
	b := make([]byte, n)
	if _, err := f.ReadAt(b, end-int64(len(tail))-n); err != nil {
		return nil
	}
	return b
}

func Append(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

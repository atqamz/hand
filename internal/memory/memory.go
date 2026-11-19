package memory

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const OperatorFile = "operator.md"

const operatorTemplate = "# Operator memory\nWrite durable preferences and constraints for the supervisor here.\n"

func Init(home string) error {
	if err := os.MkdirAll(filepath.Join(home, "memory", "projects"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(home, "memory", OperatorFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(operatorTemplate); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

type Line struct {
	N, Bytes int
}

type Core struct {
	Lines   []string
	Total   int
	Bytes   int
	Largest []Line
}

type Topic struct {
	File  string
	Bytes int64
	Title string
}

func Read(home, name string, budget int) (Core, error) {
	b, err := os.ReadFile(filepath.Join(home, "memory", name))
	if errors.Is(err, fs.ErrNotExist) {
		return Core{}, nil
	}
	if err != nil {
		return Core{}, err
	}
	c := Core{Bytes: len(b)}
	var sized []Line
	used, fits := 0, true
	for n, raw := range strings.SplitAfter(string(b), "\n") {
		used += len(raw)
		l := strings.TrimRight(raw, "\r\n")
		if strings.TrimSpace(l) == "" {
			continue
		}
		c.Total++
		sized = append(sized, Line{n + 1, len(l)})
		if fits && used > budget {
			fits = false
		}
		if fits {
			c.Lines = append(c.Lines, strings.ToValidUTF8(l, ""))
		}
	}
	sort.SliceStable(sized, func(i, j int) bool { return sized[i].Bytes > sized[j].Bytes })
	c.Largest = sized[:min(3, len(sized))]
	return c, nil
}

func Topics(home string) ([]Topic, error) {
	dir := filepath.Join(home, "memory", "operator")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Topic
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Topic{e.Name(), fi.Size(), heading(filepath.Join(dir, e.Name()))})
	}
	return out, nil
}

func heading(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), "\r"); strings.HasPrefix(l, "#") {
			return strings.ToValidUTF8(strings.TrimSpace(strings.TrimLeft(l, "#")), "")
		}
	}
	return ""
}

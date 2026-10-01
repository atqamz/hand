package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxImage = 10 << 20
	maxPost  = 11 << 20
)

var (
	shotName    = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9]+\.(png|jpg|webp|gif)$`)
	imageExt    = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}
	shotTypes   = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}
	errTooLarge = errors.New("an image can be at most 10 MiB")
	errNotImage = errors.New("only png, jpeg, webp and gif images can be attached")
)

func saveImage(dir string, now time.Time, body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxImage+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxImage {
		return "", errTooLarge
	}
	ext, ok := imageExt[http.DetectContentType(data)]
	if !ok {
		return "", errNotImage
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	stamp := now.Format("20060102-150405")
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d.%s", stamp, n, ext)
		err := os.Link(tmp.Name(), filepath.Join(dir, name))
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
}

func (b *Board) image(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	f, _, err := r.FormFile("image")
	if err != nil {
		b.fail(w, http.StatusBadRequest, "attach one image in the field image")
		return
	}
	defer f.Close()
	dir := filepath.Join(b.o.Home, "inbox")
	name, err := saveImage(dir, b.now(), f)
	switch {
	case errors.Is(err, errTooLarge):
		b.fail(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, errNotImage):
		b.fail(w, http.StatusUnsupportedMediaType, err.Error())
	case err != nil:
		b.failErr(w, err)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": filepath.Join(dir, name), "name": name})
	}
}

func (b *Board) inbox(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !shotName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	f, err := os.OpenInRoot(filepath.Join(b.o.Home, "inbox"), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", shotTypes[filepath.Ext(name)])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = io.Copy(w, f)
}

var shotLine = regexp.MustCompile(`^\[image: (/[^\]\n]+)\]$`)

func shots(text, home string) (string, []string) {
	inbox := filepath.Join(home, "inbox")
	var keep []string
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if m := shotLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if dir, name := filepath.Split(m[1]); filepath.Clean(dir) == inbox && shotName.MatchString(name) {
				names = append(names, name)
				continue
			}
		}
		keep = append(keep, line)
	}
	return strings.Trim(strings.Join(keep, "\n"), "\n"), names
}

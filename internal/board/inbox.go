package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
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
	maxName  = 80
)

var (
	shotName    = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9]+(\.(png|jpg|webp|gif)|-[A-Za-z0-9_][A-Za-z0-9._-]{0,79})$`)
	imageExt    = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}
	shotTypes   = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}
	imageLike   = regexp.MustCompile(`(?i)\.(png|jpe?g|webp|gif)$`)
	unsafeRuns  = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	errTooLarge = errors.New("a file can be at most 10 MiB")
)

func safeName(name string) string {
	name = strings.TrimRight(strings.TrimLeft(unsafeRuns.ReplaceAllString(filepath.Base(strings.ReplaceAll(name, `\`, "/")), "_"), ".-"), ".")
	if name == "" {
		return "file"
	}
	return name
}

func clipName(name string) string {
	if len(name) <= maxName {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	return strings.TrimRight(name[:maxName-len(ext)], ".") + ext
}

func fileKind(name string) string {
	if ext := strings.TrimPrefix(filepath.Ext(name), "."); ext != "" && len(ext) <= 4 {
		return strings.ToUpper(ext)
	}
	return "FILE"
}

func isImage(name string) bool {
	_, ok := shotTypes[filepath.Ext(name)]
	return ok
}

func saveImage(dir string, now time.Time, original string, body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxImage+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxImage {
		return "", errTooLarge
	}
	safe := clipName(safeName(original))
	if ext, ok := imageExt[http.DetectContentType(data)]; ok {
		safe = clipName(strings.TrimSuffix(safe, filepath.Ext(safe)) + "." + ext)
	} else if imageLike.MatchString(safe) {
		safe = clipName(safe + ".bin")
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
		name := fmt.Sprintf("%s-%d-%s", stamp, n, safe)
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
	f, h, err := r.FormFile("image")
	if err != nil {
		b.fail(w, http.StatusBadRequest, "attach one file in the field image")
		return
	}
	defer f.Close()
	dir := filepath.Join(b.o.Home, "inbox")
	name, err := saveImage(dir, b.now(), h.Filename, f)
	switch {
	case errors.Is(err, errTooLarge):
		b.fail(w, http.StatusRequestEntityTooLarge, err.Error())
	case err != nil:
		b.failErr(w, err)
	default:
		kind := "file"
		if isImage(name) {
			kind = "image"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"path": filepath.Join(dir, name), "name": name, "kind": kind})
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
	if isImage(name) {
		w.Header().Set("Content-Type", shotTypes[filepath.Ext(name)])
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": label(name)}))
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", time.Time{}, f)
}

var shotLine = regexp.MustCompile(`^\[(image|file): ([^\]\n]+)\]$`)

type attachment struct {
	Name, Label, Kind, Size, Path string
	Image, Missing                bool
}

func label(name string) string {
	if _, rest, ok := strings.Cut(name[len("20060102-150405-"):], "-"); ok {
		return rest
	}
	return name
}

func size(n int64) string {
	switch kib := (n + 1<<9) >> 10; {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case kib < 1<<10:
		return fmt.Sprintf("%d KiB", kib)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}

func shots(text, home string) (string, []attachment) {
	inbox := filepath.Join(home, "inbox")
	var keep []string
	var files []attachment
	for _, line := range strings.Split(text, "\n") {
		if m := shotLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if dir, name := filepath.Split(m[2]); filepath.Clean(dir) == inbox && shotName.MatchString(name) {
				a := attachment{Name: name, Label: label(name), Path: m[2], Image: isImage(name), Kind: fileKind(label(name))}
				if fi, err := os.Lstat(filepath.Join(inbox, name)); err == nil && fi.Mode().IsRegular() {
					a.Size = size(fi.Size())
				} else {
					a.Missing = true
				}
				files = append(files, a)
				continue
			}
		}
		keep = append(keep, line)
	}
	return strings.Trim(strings.Join(keep, "\n"), "\n"), files
}

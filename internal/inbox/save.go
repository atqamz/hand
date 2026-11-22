package inbox

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxImage   = 10 << 20
	MaxName    = 80
	MaxPixels  = 36_000_000
	StaleTemp  = time.Hour
	tempPrefix = ".upload-"
)

var (
	ErrTooLarge = errors.New("a file can be at most 10 MiB")
	ErrPixels   = errors.New("an image can be at most 36 megapixels")
	ErrUnsized  = errors.New("an image whose size cannot be read is refused")

	ImageExt   = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}
	ImageLike  = regexp.MustCompile(`(?i)\.(png|jpe?g|webp|gif)$`)
	unsafeRuns = regexp.MustCompile(`[^\p{L}\p{M}\p{N}._-]+`)
	invisible  = []*unicode.RangeTable{unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector}
)

func TooBig(err error) bool {
	return errors.Is(err, ErrTooLarge) || errors.Is(err, ErrPixels) || errors.Is(err, ErrUnsized)
}

func SafeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsOneOf(invisible, r) {
			return ' '
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimLeft(unsafeRuns.ReplaceAllString(filepath.Base(strings.ReplaceAll(name, `\`, "/")), "_"), ".-"), ".")
	if name == "" {
		return "file"
	}
	return name
}

func ClipName(name string) string {
	if len(name) <= MaxName {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	cut := MaxName - len(ext)
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return strings.TrimRight(name[:cut], ".") + ext
}

func Pixels(data []byte) (int64, bool) {
	if http.DetectContentType(data) == "image/webp" {
		return webpPixels(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	return int64(cfg.Width) * int64(cfg.Height), err == nil
}

func webpPixels(b []byte) (int64, bool) {
	if len(b) < 25 {
		return 0, false
	}
	var w, h uint32
	switch string(b[12:16]) {
	case "VP8 ":
		if len(b) < 30 || b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, false
		}
		w, h = uint32(binary.LittleEndian.Uint16(b[26:]))&0x3fff, uint32(binary.LittleEndian.Uint16(b[28:]))&0x3fff
	case "VP8L":
		if b[20] != 0x2f {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(b[21:])
		w, h = v&0x3fff+1, v>>14&0x3fff+1
	case "VP8X":
		if len(b) < 30 {
			return 0, false
		}
		w = uint32(b[24]) | uint32(b[25])<<8 | uint32(b[26])<<16 + 1
		h = uint32(b[27]) | uint32(b[28])<<8 | uint32(b[29])<<16 + 1
	default:
		return 0, false
	}
	return int64(w) * int64(h), true
}

func Save(dir string, now time.Time, original string, body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxImage+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxImage {
		return "", ErrTooLarge
	}
	safe := ClipName(SafeName(original))
	if ext, ok := ImageExt[http.DetectContentType(data)]; ok {
		px, known := Pixels(data)
		if !known {
			return "", ErrUnsized
		}
		if px > MaxPixels {
			return "", ErrPixels
		}
		safe = ClipName(strings.TrimSuffix(safe, filepath.Ext(safe)) + "." + ext)
	} else if ImageLike.MatchString(safe) {
		safe = ClipName(safe + ".bin")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	SweepUploads(dir, now, false)
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	stamp := now.Format("20060102-150405")
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d-%s", stamp, n, safe)
		switch err := os.Link(tmp.Name(), filepath.Join(dir, name)); {
		case errors.Is(err, fs.ErrExist):
			continue
		case err != nil:
			return "", err
		}
		return name, nil
	}
}

func SweepUploads(dir string, now time.Time, dry bool) ([]File, []Failure) {
	des, _ := os.ReadDir(dir)
	var out []File
	var failed []Failure
	for _, de := range des {
		if !strings.HasPrefix(de.Name(), tempPrefix) {
			continue
		}
		info, err := de.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) <= StaleTemp {
			continue
		}
		if !dry {
			if err := os.Remove(filepath.Join(dir, de.Name())); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					failed = append(failed, Failure{de.Name(), err})
				}
				continue
			}
		}
		out = append(out, File{de.Name(), info.Size(), info.ModTime()})
	}
	return out, failed
}

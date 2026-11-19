package launcher

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/atqamz/hand/internal/brand"
	"github.com/atqamz/hand/internal/desktopasset"
)

const (
	bundleKey = "HandManaged"
	pkgInfo   = "APPL????"
	iconName  = "Hand.icns"
)

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Hand</string>
	<key>CFBundleDisplayName</key>
	<string>Hand</string>
	<key>CFBundleIdentifier</key>
	<string>io.github.atqamz.hand</string>
	<key>CFBundleExecutable</key>
	<string>hand-desktop</string>
	<key>CFBundleIconFile</key>
	<string>Hand</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleSignature</key>
	<string>????</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>HandManaged</key>
	<true/>
</dict>
</plist>
`

type bundle struct{ app string }

func bundleAt(home string) (bundle, error) {
	if !filepath.IsAbs(home) {
		return bundle{}, errors.New("launcher: HOME is not an absolute path")
	}
	return bundle{filepath.Join(home, "Applications", desktopasset.Bundle)}, nil
}

func (b bundle) plist() string  { return filepath.Join(b.app, "Contents", "Info.plist") }
func (b bundle) info() string   { return filepath.Join(b.app, "Contents", "PkgInfo") }
func (b bundle) icon() string   { return filepath.Join(b.app, "Contents", "Resources", iconName) }
func (b bundle) hand() string   { return filepath.Join(b.app, "Contents", "MacOS", "hand") }
func (b bundle) window() string { return filepath.Join(b.app, "Contents", "MacOS", desktopasset.Name) }

func (b bundle) managed() (owned, exists bool) {
	if _, err := os.Lstat(b.app); err != nil {
		return false, !errors.Is(err, fs.ErrNotExist)
	}
	have, err := os.ReadFile(b.plist())
	return err == nil && marked(have), true
}

func marked(plist []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(plist))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth != 3 || t.Name.Local != "key" {
				continue
			}
			var name string
			if dec.DecodeElement(&name, &t) != nil {
				return false
			}
			depth--
			if name == bundleKey {
				return true
			}
		case xml.EndElement:
			depth--
		}
	}
}

func link(path, target string) error {
	if have, err := os.Readlink(path); err == nil && have == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".hand-desktop.link-"+rand.Text())
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func bundleSync(home, bin, hand string) (skipped string, err error) {
	if !filepath.IsAbs(hand) || strings.ContainsAny(hand, "\n\r") {
		return "", fmt.Errorf("launcher: %q is not a usable absolute path", hand)
	}
	b, err := bundleAt(home)
	if err != nil {
		return "", err
	}
	owned, existed := b.managed()
	if existed && !owned {
		return b.app, nil
	}
	if bin != b.window() {
		return "", fmt.Errorf("launcher: %s is not %s", bin, b.window())
	}
	defer func() {
		if err != nil && !existed {
			_ = os.RemoveAll(b.app)
		}
	}()
	for _, f := range []struct {
		path string
		want []byte
	}{{b.plist(), []byte(infoPlist)}, {b.info(), []byte(pkgInfo)}, {b.icon(), icnsFile()}} {
		if _, err := put(f.path, f.want); err != nil {
			return "", fmt.Errorf("launcher: cannot write %s: %w", f.path, err)
		}
	}
	if err := link(b.hand(), hand); err != nil {
		return "", fmt.Errorf("launcher: cannot write %s: %w", b.hand(), err)
	}
	return "", nil
}

func bundleRemove(home string) (skipped string, err error) {
	b, err := bundleAt(home)
	if err != nil {
		return "", err
	}
	owned, exists := b.managed()
	if !exists {
		return "", nil
	}
	if !owned {
		return b.app, nil
	}
	if err := os.RemoveAll(b.app); err != nil {
		return "", fmt.Errorf("launcher: cannot remove %s: %w", b.app, err)
	}
	return "", nil
}

func bundleCheck(home, bin, hand string) (State, string) {
	b, err := bundleAt(home)
	if err != nil {
		return Missing, ""
	}
	owned, exists := b.managed()
	switch {
	case !exists:
		return Missing, b.app
	case !owned:
		return Foreign, b.app
	}
	if have, _ := os.ReadFile(b.plist()); string(have) != infoPlist || bin != b.window() {
		return Elsewhere, b.app
	}
	if target, err := os.Readlink(b.hand()); err != nil || target != hand {
		return Elsewhere, b.hand()
	}
	for _, p := range []string{b.info(), b.icon(), b.window()} {
		if _, err := os.Stat(p); err != nil {
			return Missing, p
		}
	}
	return OK, b.app
}

var icnsFile = sync.OnceValue(func() []byte {
	src, err := png.Decode(bytes.NewReader(brand.Icon512))
	if err != nil {
		panic("launcher: the embedded icon is not a PNG: " + err.Error())
	}
	var pngs [513][]byte
	for _, n := range []int{16, 32, 64, 128, 256} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, shrink(src, n)); err != nil {
			panic(err)
		}
		pngs[n] = buf.Bytes()
	}
	pngs[512] = brand.Icon512
	var body []byte
	for _, e := range []struct {
		kind string
		size int
	}{{"icp4", 16}, {"icp5", 32}, {"icp6", 64}, {"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512}} {
		body = append(body, e.kind...)
		body = binary.BigEndian.AppendUint32(body, uint32(8+len(pngs[e.size])))
		body = append(body, pngs[e.size]...)
	}
	out := append([]byte("icns"), binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))...)
	return append(out, body...)
})

package kit

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

const (
	Sans = "Noto Sans"
	Mono = "JetBrains Mono"
)

//go:embed fonts
var fontFiles embed.FS

var fontsRegistered = registerFonts()

func registerFonts() error {
	for _, f := range [...]struct{ file, family string }{
		{"NotoSans-Regular.ttf", Sans}, {"NotoSans-SemiBold.ttf", Sans},
		{"JetBrainsMono-Regular.ttf", Mono}, {"JetBrainsMono-SemiBold.ttf", Mono},
	} {
		data, err := fontFiles.ReadFile("fonts/" + f.file)
		if err != nil {
			return err
		}
		if err := ui.RegisterFont(data, f.family); err != nil {
			return err
		}
	}
	return nil
}

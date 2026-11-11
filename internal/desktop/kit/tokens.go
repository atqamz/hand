package kit

import "github.com/egoist/mygo/ui"

type Colors struct {
	BG         ui.Color `css:"bg"`
	Panel      ui.Color `css:"panel"`
	Card       ui.Color `css:"card"`
	FG         ui.Color `css:"fg"`
	Muted      ui.Color `css:"muted"`
	Line       ui.Color `css:"line"`
	LineStrong ui.Color `css:"line-strong"`
	Hover      ui.Color `css:"hover"`
	Fail       ui.Color `css:"fail"`
	Wait       ui.Color `css:"wait"`
	Pass       ui.Color `css:"pass"`
	Neutral    ui.Color `css:"neutral"`
	Accent     ui.Color `css:"accent"`
	FailBG     ui.Color `css:"fail-bg"`
	WaitBG     ui.Color `css:"wait-bg"`
	PassBG     ui.Color `css:"pass-bg"`
	NeutralBG  ui.Color `css:"neutral-bg"`
	AccentBG   ui.Color `css:"accent-bg"`
	Screen     ui.Color `css:"screen"`
	ScreenFG   ui.Color `css:"screen-fg"`
	Primary    ui.Color `css:"primary"`
	OnPrimary  ui.Color `css:"on-primary"`
	Shadow     ui.Color `css:"shadow"`
	Tint       [12]ui.Color
}

func ColorsFor(dark bool) Colors {
	if dark {
		return Colors{
			BG: ui.Hex("#111317"), Panel: ui.Hex("#15181d"), Card: ui.Hex("#1b1f25"), FG: ui.Hex("#e8eaee"), Muted: ui.Hex("#9ba2ad"),
			Line: ui.Hex("#262b32"), LineStrong: ui.Hex("#6b7482"), Hover: ui.Hex("#1e232a"),
			Fail: ui.Hex("#ff6b6b"), Wait: ui.Hex("#e6ac45"), Pass: ui.Hex("#56c77a"), Neutral: ui.Hex("#9ba2ad"), Accent: ui.Hex("#7ea6ff"),
			FailBG: ui.Hex("#2c1719"), WaitBG: ui.Hex("#2a2212"), PassBG: ui.Hex("#13281b"), NeutralBG: ui.Hex("#1e232a"), AccentBG: ui.Hex("#172238"),
			Screen: ui.Hex("#08090b"), ScreenFG: ui.Hex("#d8dde4"), Primary: ui.Hex("#e8eaee"), OnPrimary: ui.Hex("#111317"), Shadow: ui.RGBA(0, 0, 0, .55),
			Tint: [12]ui.Color{
				ui.Hex("#5b8fe0"), ui.Hex("#8b82ea"), ui.Hex("#b06fe3"), ui.Hex("#d955cc"), ui.Hex("#e0628f"), ui.Hex("#e06a5e"),
				ui.Hex("#c18a3c"), ui.Hex("#9aa13a"), ui.Hex("#6ea73d"), ui.Hex("#45ad50"), ui.Hex("#3fac7c"), ui.Hex("#3fa6b0"),
			},
		}
	}
	return Colors{
		BG: ui.Hex("#ffffff"), Panel: ui.Hex("#f6f7f9"), Card: ui.Hex("#f1f3f5"), FG: ui.Hex("#15171c"), Muted: ui.Hex("#5b626e"),
		Line: ui.Hex("#e3e6ea"), LineStrong: ui.Hex("#80889a"), Hover: ui.Hex("#eef0f3"),
		Fail: ui.Hex("#c42b32"), Wait: ui.Hex("#8a5b00"), Pass: ui.Hex("#1a7a3e"), Neutral: ui.Hex("#5b626e"), Accent: ui.Hex("#2457c5"),
		FailBG: ui.Hex("#fdeeee"), WaitBG: ui.Hex("#fcf3dc"), PassBG: ui.Hex("#e4f4e9"), NeutralBG: ui.Hex("#eef0f3"), AccentBG: ui.Hex("#ebf1fd"),
		Screen: ui.Hex("#111418"), ScreenFG: ui.Hex("#d8dde4"), Primary: ui.Hex("#15171c"), OnPrimary: ui.Hex("#ffffff"), Shadow: ui.RGBA(21, 23, 28, .12),
		Tint: [12]ui.Color{
			ui.Hex("#407acc"), ui.Hex("#736ad7"), ui.Hex("#9f58d3"), ui.Hex("#ca38be"), ui.Hex("#ce4880"), ui.Hex("#cd4f44"),
			ui.Hex("#a1702b"), ui.Hex("#777f22"), ui.Hex("#4d8724"), ui.Hex("#258b2d"), ui.Hex("#24895f"), ui.Hex("#25848d"),
		},
	}
}

const (
	RadiusXS   = 3
	RadiusSM   = 4
	RadiusMD   = 6
	RadiusLG   = 10
	RadiusPill = 999

	Control = 32
	Badge   = 20
	Mast    = 48

	Space2  = 2
	Space4  = 4
	Space6  = 6
	Space8  = 8
	Space10 = 10
	Space12 = 12
	Space14 = 14
	Space16 = 16
	Space20 = 20
	Space24 = 24

	RowSM = 36
	Row   = 40
	Tap   = 44
	RowLG = 48
	Sheet = 52
)

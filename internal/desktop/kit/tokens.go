package kit

import "github.com/egoist/mygo/ui"

type Colors struct {
	BG         ui.Color
	Panel      ui.Color
	Card       ui.Color
	FG         ui.Color
	Muted      ui.Color
	Line       ui.Color
	LineStrong ui.Color
	Hover      ui.Color
	Fail       ui.Color
	Wait       ui.Color
	Pass       ui.Color
	Neutral    ui.Color
	Accent     ui.Color
	FailBG     ui.Color
	WaitBG     ui.Color
	PassBG     ui.Color
	NeutralBG  ui.Color
	AccentBG   ui.Color
	Screen     ui.Color
	ScreenFG   ui.Color
	Primary    ui.Color
	OnPrimary  ui.Color
	Shadow     ui.Color
	Backdrop   ui.Color
	Shell      ui.Color
	ShellLine  ui.Color
	Ticket     ui.Color
	Tint       [12]ui.Color
}

func ColorsFor(dark bool) Colors {
	if dark {
		return Colors{
			BG: ui.Hex("#1a1e22"), Panel: ui.Hex("#131619"), Card: ui.Hex("#23292e"), FG: ui.Hex("#e8ebee"), Muted: ui.Hex("#b1b9c1"),
			Line: ui.Hex("#2b3137"), LineStrong: ui.Hex("#7a848e"), Hover: ui.Hex("#21262b"),
			Fail: ui.Hex("#ff9ca0"), Wait: ui.Hex("#f0b03a"), Pass: ui.Hex("#5fcd89"), Neutral: ui.Hex("#b1b9c1"), Accent: ui.Hex("#9dbbff"),
			FailBG: ui.Hex("#3b1a1e"), WaitBG: ui.Hex("#33270e"), PassBG: ui.Hex("#123020"), NeutralBG: ui.Hex("#23292e"), AccentBG: ui.Hex("#1a2947"),
			Screen: ui.Hex("#0b0d0f"), ScreenFG: ui.Hex("#d6dce2"), Primary: ui.Hex("#e8ebee"), OnPrimary: ui.Hex("#14171a"), Shadow: ui.RGBA(0, 0, 0, .55), Backdrop: ui.RGBA(0, 0, 0, .32),
			Shell: ui.Hex("#242a2f"), ShellLine: ui.Hex("#323a41"), Ticket: ui.Hex("#2b3137"),
			Tint: [12]ui.Color{
				ui.Hex("#5b8fe0"), ui.Hex("#8b82ea"), ui.Hex("#b06fe3"), ui.Hex("#d955cc"), ui.Hex("#e0628f"), ui.Hex("#e06a5e"),
				ui.Hex("#c18a3c"), ui.Hex("#9aa13a"), ui.Hex("#6ea73d"), ui.Hex("#45ad50"), ui.Hex("#3fac7c"), ui.Hex("#3fa6b0"),
			},
		}
	}
	return Colors{
		BG: ui.Hex("#ffffff"), Panel: ui.Hex("#eef1f3"), Card: ui.Hex("#e6eaed"), FG: ui.Hex("#14171a"), Muted: ui.Hex("#535b63"),
		Line: ui.Hex("#dce0e4"), LineStrong: ui.Hex("#6c757e"), Hover: ui.Hex("#f1f3f5"),
		Fail: ui.Hex("#c0182b"), Wait: ui.Hex("#8a5200"), Pass: ui.Hex("#16703a"), Neutral: ui.Hex("#535b63"), Accent: ui.Hex("#1d55c4"),
		FailBG: ui.Hex("#fde9eb"), WaitBG: ui.Hex("#ffefcf"), PassBG: ui.Hex("#e2f2e7"), NeutralBG: ui.Hex("#eceff1"), AccentBG: ui.Hex("#e5edfc"),
		Screen: ui.Hex("#111417"), ScreenFG: ui.Hex("#d6dce2"), Primary: ui.Hex("#14171a"), OnPrimary: ui.Hex("#ffffff"), Shadow: ui.RGBA(20, 23, 26, .12), Backdrop: ui.RGBA(0, 0, 0, .32),
		Shell: ui.Hex("#d3d9de"), ShellLine: ui.Hex("#b3bbc2"), Ticket: ui.Hex("#ffffff"),
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

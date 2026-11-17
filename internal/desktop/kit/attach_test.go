package kit

import (
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAttachChipMatchesTheBoardsChip(t *testing.T) {
	wantDecls(t, ".att", "grid-template-columns:auto minmax(0,1fr) auto", "column-gap:var(--space-8)", "max-width:min(100%,280px)",
		"padding:var(--space-2) var(--space-2) var(--space-2) var(--space-8)", "border:1px solid var(--line)", "border-radius:var(--r-md)",
		"background:var(--panel)", "line-height:20px")
	wantDecls(t, ".att-meta", "color:var(--muted)", "font-size:12px")
	wantDecls(t, ".att-x", "width:var(--control)")
	wantDecls(t, ".att[data-tone=fail]", "border-color:var(--fail)", "background:var(--fail-bg)")
	wantDecls(t, ".att[data-tone=fail]>.att-meta", "color:var(--fail)", "line-height:16px")
	wantDecls(t, ".attachments", "gap:var(--space-6)", "padding:var(--space-2) var(--space-6) var(--space-8)")
	if AttachMax != 280 {
		t.Errorf("AttachMax = %d", AttachMax)
	}
	tt := host(func(c *ui.Context) {
		ui.Row(c).Label("row").Children(func() {
			AttachChip(c, ColorsFor(false), Chip{Key: "a", Badge: "PNG", Name: "shot.png", Meta: "PNG · 1 KiB", Remove: "Remove shot.png"})
		})
	})
	if got := rect(t, tt, "Attachment shot.png").H; got != 2*Space2+2*Body.Line+2 {
		t.Errorf("a chip is %v tall, want 46", got)
	}
	if got := rect(t, tt, "Remove shot.png").W; got != Control {
		t.Errorf("the remove button is %v wide, want %v", got, float32(Control))
	}
	fail := host(func(c *ui.Context) {
		AttachChip(c, ColorsFor(false), Chip{Key: "a", Badge: "PNG", Name: "shot.png", Meta: "a file can be at most 10 MiB", Tone: ChipFail})
	})
	if got := rect(t, fail, "Attachment shot.png").H; got != 2*Space2+Body.Line+16+2 {
		t.Errorf("a failed chip is %v tall, want 42", got)
	}
}

func TestSentFileMatchesTheBoardsFileChip(t *testing.T) {
	wantDecls(t, ".shots .sent-file", "column-gap:var(--space-10)", "max-width:min(100%,280px)",
		"padding:var(--space-2) var(--space-12) var(--space-2) var(--space-8)", "border:1px solid var(--line)", "border-radius:var(--r-md)",
		"background:var(--bg)", "line-height:20px")
	wantDecls(t, ".shots .sent-file.missing", "border-style:dashed", "color:var(--muted)")
	tt := host(func(c *ui.Context) {
		SentFile(c, ColorsFor(false), "f", "LOG", "server.log", "LOG · 1.4 MiB", false)
	})
	if got := rect(t, tt, "server.log").H; got != 2*Space2+2*Body.Line+2 {
		t.Errorf("a file chip is %v tall, want 46", got)
	}
}

func TestSentPictureFitsTheBoardsBox(t *testing.T) {
	wantDecls(t, ".shots img", "max-width:240px", "max-height:180px", "border:1px solid var(--line)", "border-radius:var(--r-md)", "background:var(--card)")
	if ShotWidth != 240 || ShotHeight != 180 {
		t.Errorf("the box is %dx%d", ShotWidth, ShotHeight)
	}
	for _, tc := range []struct {
		w, h, ww, hh int
	}{{480, 270, 240, 136}, {100, 50, 102, 52}, {100, 400, 46, 180}} {
		img := ui.MustParseSVG([]byte(`<svg viewBox="0 0 1 1" width="` + strconv.Itoa(tc.w) + `" height="` + strconv.Itoa(tc.h) + `"><path d="M0 0h1v1z"/></svg>`))
		tt := host(func(c *ui.Context) {
			ui.Row(c).AlignItems(ui.Start).Children(func() { SentPicture(c, ColorsFor(false), "p", img, "pic") })
		})
		r := rect(t, tt, "Attached image pic")
		if r.W > ShotWidth || r.H > ShotHeight || r.W < float32(tc.ww)-2 || r.W > float32(tc.ww)+2 {
			t.Errorf("a %dx%d picture is drawn %vx%v, want about %dx%d inside the %dx%d box", tc.w, tc.h, r.W, r.H, tc.ww, tc.hh, ShotWidth, ShotHeight)
		}
	}
}

func TestPaperclipMatchesTheBoardsAttachButton(t *testing.T) {
	wantDecls(t, ".attach", "width:var(--control)", "height:var(--control)", "border:1px solid var(--line-strong)", "border-radius:var(--r-md)",
		"background:var(--bg)", "color:var(--muted)")
	tt := host(func(c *ui.Context) { IconButton(c, ColorsFor(false), "a", "Attach files", "attach") })
	r := rect(t, tt, "Attach files")
	if r.W != Control || r.H != Control {
		t.Errorf("the paperclip is %vx%v, want %d square", r.W, r.H, Control)
	}
}

func TestDropVeilMatchesTheBoardsVeil(t *testing.T) {
	wantDecls(t, ".drop-veil", "inset:8px", "gap:var(--space-6)", "padding:var(--space-24)", "border:2px dashed var(--accent)", "border-radius:var(--r-lg)")
	wantDecls(t, ".drop-title", "font-size:15px", "font-weight:600", "color:var(--accent)")
	wantDecls(t, ".drop-rule", "font-size:12px", "color:var(--muted)")
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { DropVeil(c, ColorsFor(false)) })
	}, 600, 400)
	r := rect(t, tt, "Drop to attach")
	if r.X != 8 || r.Y != 8 || r.W != 584 || r.H != 384 {
		t.Errorf("the veil is %v, want the pane inset by 8", r)
	}
	if !tt.HasText("Each file is copied to this fleet's inbox/, up to 10 MiB. Folders: paste their path.") {
		t.Errorf("the veil says %q", tt.Texts())
	}
}

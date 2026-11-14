package kit

import "github.com/egoist/mygo/ui"

type Choice struct {
	Value       string
	Key         string
	Label       string
	Note        string
	Recommended bool
	Long        bool
	Picked      bool
	Answer      bool
	Disabled    bool
}

func ChoiceList(c *ui.Context, k Colors, choices []Choice) (picked string) {
	if len(choices) == 0 {
		return ""
	}
	list := ui.Column(c).Border(1, k.Line).Radius(RadiusLG).Background(k.BG).Clip().Label("Options")
	list.Children(func() {
		for i, choice := range choices {
			row := ui.ButtonBase(c.Key("choice:"+choice.Value)).
				MinHeight(RowLG).Padding(Space12, Space14).Gap(Space12).
				AlignItems(ui.Start).Disabled(choice.Disabled)
			if choice.Picked {
				row.Background(k.AccentBG)
			}
			if i > 0 {
				row.Padding(Space12-1, Space14, Space12).BorderWidth(1, 0, 0, 0).BorderColor(k.Line)
			}
			if !choice.Disabled {
				row.Cursor(ui.CursorPointer)
			}
			row.Children(func() {
				circle := ui.Row(c).Size(24, 24).Radius(RadiusPill).
					Justify(ui.Center).AlignItems(ui.Center)
				fg := k.Muted
				if choice.Picked {
					circle.Background(k.Accent).Border(1, k.Accent)
					fg = k.BG
				} else {
					circle.Border(1, k.LineStrong)
				}
				circle.Children(func() {
					ui.Text(c, choice.Key).Font("monospace").FontSize(Meta.Size).Bold().TextColor(fg)
				})
				ui.Column(c).Gap(Space2).Grow(1).MinWidth(0).Children(func() {
					ui.Row(c).Gap(Space12).AlignItems(ui.Center).Children(func() {
						label := ui.Text(c, choice.Label).FontSize(Body.Size).Grow(1).MinWidth(0)
						if !choice.Long {
							label.Bold()
						}
						switch {
						case choice.Answer:
							ui.Badge(c, "Your answer").Background(k.AccentBG).TextColor(k.Accent)
						case choice.Recommended:
							ui.Badge(c, "Recommended").Background(k.PassBG).TextColor(k.Pass)
						}
					})
					if choice.Note != "" {
						ui.Text(c, choice.Note).FontSize(Body.Size).TextColor(k.Muted)
					}
				})
			})
			if !choice.Disabled && row.Clicked() {
				picked = choice.Value
			}
		}
	})
	return picked
}

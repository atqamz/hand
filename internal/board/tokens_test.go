package board_test

import (
	"regexp"
	"strings"
	"testing"
)

var (
	cssToken = regexp.MustCompile(`--[\w-]+\s*:[^;}]*`)
	cssMask  = regexp.MustCompile(`(?:^|[;{])\s*mask(?:-image)?\s*:[^;}]*`)
	cssPaint = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)
	cssSize  = regexp.MustCompile(`(?:^|[;{])\s*(height|min-height|max-height|padding(?:-(?:block|inline|top|right|bottom|left))?|gap|row-gap|column-gap|margin(?:-(?:block|inline|top|right|bottom|left))?)\s*:\s*([^;}]+)`)
	cssPx    = regexp.MustCompile(`-?\d+(?:\.\d+)?px`)
)

var (
	// Values still raw on purpose: 1px hairlines, icon and scrollbar geometry,
	// centring gutters, and the odd historic steps a token would only rename.
	rawHeights = map[string]bool{"1": true, "2": true, "5": true, "6": true, "8": true, "12": true, "16": true, "18": true, "22": true, "24": true, "32": true, "61": true, "96": true, "120": true, "180": true, "260": true, "300": true, "320": true}
	rawSpacing = map[string]bool{
		"1": true, "3": true, "5": true, "7": true, "9": true, "11": true, "13": true, "15": true,
		"22": true, "26": true, "32": true, "40": true, "34": true, "41": true, "48": true, "56": true,
		"760": true, "848": true, "1088": true,
		"-1": true, "-4": true, "-6": true, "-7": true, "-8": true, "-10": true, "-13": true, "-18": true, "-20": true,
	}
)

func TestTheBoardCSSPaintsOnlyWithTokens(t *testing.T) {
	css := asset(t, "board.css")
	bare := cssMask.ReplaceAllString(cssToken.ReplaceAllString(css, ""), "")
	for _, paint := range cssPaint.FindAllString(bare, -1) {
		t.Errorf("board.css paints %q outside a custom property; add a token in :root", paint)
	}
}

func TestTheBoardCSSSizesOnlyWithTokens(t *testing.T) {
	css := asset(t, "board.css")
	bare := cssToken.ReplaceAllString(css, "")
	for _, m := range cssSize.FindAllStringSubmatch(bare, -1) {
		prop, value := m[1], m[2]
		tokens := rawSpacing
		if strings.Contains(prop, "height") {
			tokens = rawHeights
		}
		for _, px := range cssPx.FindAllString(value, -1) {
			if n := strings.TrimSuffix(px, "px"); !tokens[n] {
				t.Errorf("board.css sets %s:%s; %spx needs a token or a place on the allowlist", prop, strings.TrimSpace(value), n)
			}
		}
	}
}

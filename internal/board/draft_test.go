package board_test

import (
	"strings"
	"testing"
)

func TestTheDraftSurvivesInPlaceNavigation(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		"const composerKey = `hand.draft${base}`;",
		"sessionStorage.getItem(composerKey)", "sessionStorage.setItem(composerKey, value)", "sessionStorage.removeItem(composerKey)",
		"} catch {}",
		`if (!message.value) message.value = kept();`,
		`message.addEventListener("input", () => kept(message.value));`,
		"form.reset();\n\t\t\t\tkept(\"\");")
	lacks(t, "app.js", js, "localStorage.getItem(composerKey)", "localStorage.setItem(composerKey")
	if strings.Count(js, "sessionStorage") != 3 {
		t.Fatalf("app.js reaches sessionStorage %d times, want 3", strings.Count(js, "sessionStorage"))
	}
}

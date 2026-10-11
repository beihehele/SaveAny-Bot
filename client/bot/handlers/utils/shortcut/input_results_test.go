package shortcut

import (
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/pkg/inputresult"
)

func TestInputWarningsAreBoundedTranslatedAndRetainOriginalPositions(t *testing.T) {
	for _, locale := range []string{"zh-Hans", "en"} {
		i18n.Init(locale)
		var report inputresult.Report
		report.Add(1, "ready", 3)
		if inputWarnings(report) != "" {
			t.Fatal("successful selections generated warning")
		}
		report.Add(2, "ready", 0)
		report.Add(3, "unsupported_media", 1)
		for index := 4; index <= 22; index++ {
			report.Add(index, "message_unavailable", 0)
		}
		text := inputWarnings(report)
		if strings.Contains(text, "bot.msg.") || !strings.Contains(text, "2") || !strings.Contains(text, "3") || len(strings.Split(text, "\n")) != 12 || len([]rune(text)) > 2000 {
			t.Fatalf("unusable warning: %s", text)
		}
	}
}

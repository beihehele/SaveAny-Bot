package handlers

import (
	"regexp"
	"testing"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
)

func TestTelegramOnlyCommandsAndHelp(t *testing.T) {
	expected := map[string]bool{}
	for _, name := range []string{"start", "silent", "storage", "dir", "rule", "save", "task", "cancel", "config", "fnametmpl", "help", "watch", "copy", "unwatch", "lswatch", "lschannel", "lsgroup", "lstopic", "syncpeers"} {
		expected[name] = true
	}
	if len(CommandHandlers) != len(expected) {
		t.Fatalf("registered commands=%d", len(CommandHandlers))
	}
	seen := map[string]bool{}
	for _, command := range CommandHandlers {
		if !expected[command.Cmd] || seen[command.Cmd] {
			t.Fatalf("unexpected or duplicate command: %s", command.Cmd)
		}
		seen[command.Cmd] = true
	}
	t.Cleanup(func() { i18n.Init("zh-Hans") })
	for _, locale := range []string{"en", "zh-Hans"} {
		i18n.Init(locale)
		text := i18n.T(i18nk.BotMsgHelpTextFmt)
		listed := map[string]bool{}
		for _, match := range regexp.MustCompile(`(?m)^\s*/(\w+)\b`).FindAllStringSubmatch(text, -1) {
			if !expected[match[1]] {
				t.Fatalf("%s help advertises removed command %s", locale, match[1])
			}
			listed[match[1]] = true
		}
		for name := range expected {
			if !listed[name] {
				t.Errorf("%s help omits /%s", locale, name)
			}
		}
	}
}

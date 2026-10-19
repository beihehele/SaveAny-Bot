package handlers

import (
	"testing"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
)

func TestParseCollectArgs(t *testing.T) {
	const link = "https://t.me/c/111/12"
	for _, tc := range []struct {
		args   []string
		valid  bool
		target int64
		topic  int
	}{
		{[]string{link}, true, 0, 0},
		{[]string{link, "-1000000000222"}, true, -1000000000222, 0},
		{[]string{link, "-1000000000222", "2"}, true, -1000000000222, 2},
		{[]string{link, "-1000000000222", "0"}, true, -1000000000222, 0},
		{nil, false, 0, 0},
		{[]string{"bad"}, false, 0, 0},
		{[]string{"x" + link}, false, 0, 0},
		{[]string{link, "0"}, false, 0, 0},
		{[]string{link, "222"}, false, 0, 0},
		{[]string{link, "@target"}, false, 0, 0},
		{[]string{link, "-1000000000222:2"}, false, 0, 0},
		{[]string{link, "-1000000000222", "-1"}, false, 0, 0},
		{[]string{link, "-1000000000222", "2147483648"}, false, 0, 0},
		{[]string{link, "-1000000000222", "2", "extra"}, false, 0, 0},
	} {
		got, err := parseCollectArgs(tc.args)
		if (err == nil) != tc.valid {
			t.Fatalf("args=%v error=%v", tc.args, err)
		}
		if tc.valid && (got.URL != link || got.Target != tc.target || got.Topic != tc.topic) {
			t.Fatalf("bad args: %+v", got)
		}
	}
}

func TestCollectCommandIsRegisteredAndLocalized(t *testing.T) {
	found := false
	for _, cmd := range CommandHandlers {
		if cmd.Cmd == "collect" {
			found = true
			if cmd.handler == nil || cmd.Desc != i18nk.BotMsgCmdCollect {
				t.Fatal("collect command is incomplete")
			}
		}
	}
	if !found {
		t.Fatal("collect missing from command menu")
	}
	for _, lang := range []string{"zh-Hans", "en"} {
		i18n.Init(lang)
		if text := i18n.T(i18nk.BotMsgCollectHelpText); text == string(i18nk.BotMsgCollectHelpText) || text == "" {
			t.Fatalf("missing help in %s", lang)
		}
	}
}

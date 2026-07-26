package handlers

import (
	"testing"
)

func TestParseWatchArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string // 已去掉命令名，从 source 起
		wantSource string
		wantTarget string // 原始第二段；空表示省略
		wantFilter string
		wantErr    bool
		// 解析后语义由 handler 再调 ParseChatID；这里只测分段
		targetIsFilter bool // 第二段被当成 filter
		omitTarget     bool
	}{
		{name: "source only", args: []string{"-1001"}, wantSource: "-1001", omitTarget: true},
		{name: "source+filter", args: []string{"-1001", "msgre:a|b"}, wantSource: "-1001", wantFilter: "msgre:a|b", targetIsFilter: true, omitTarget: true},
		{name: "source+target", args: []string{"-1001", "-1002"}, wantSource: "-1001", wantTarget: "-1002"},
		{name: "source+target+topic", args: []string{"-1001", "-1002:12345"}, wantSource: "-1001", wantTarget: "-1002:12345"},
		{name: "source+target+topic+filter", args: []string{"-1001", "-1002:12345", "msgre:a|b"}, wantSource: "-1001", wantTarget: "-1002:12345", wantFilter: "msgre:a|b"},
		{name: "source+zero+filter", args: []string{"-1001", "0", "msgre:a|b"}, wantSource: "-1001", wantTarget: "0", wantFilter: "msgre:a|b"},
		{name: "source+target+filter", args: []string{"-1001", "-1002", "msgre:a|b"}, wantSource: "-1001", wantTarget: "-1002", wantFilter: "msgre:a|b"},
		{name: "empty", args: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWatchArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.SourceArg != tt.wantSource {
				t.Fatalf("source=%q want %q", got.SourceArg, tt.wantSource)
			}
			if tt.omitTarget || tt.targetIsFilter {
				if got.TargetArg != "" || !got.TargetOmitted {
					t.Fatalf("expected omitted target, got %+v", got)
				}
			} else if got.TargetArg != tt.wantTarget || got.TargetOmitted {
				t.Fatalf("target=%q omitted=%v want %q", got.TargetArg, got.TargetOmitted, tt.wantTarget)
			}
			if got.FilterArg != tt.wantFilter {
				t.Fatalf("filter=%q want %q", got.FilterArg, tt.wantFilter)
			}
		})
	}
}

func TestParseTargetWithTopic(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		wantChat  string
		wantTopic int
		wantErr   bool
	}{
		{name: "plain target", arg: "-1002", wantChat: "-1002"},
		{name: "target with topic", arg: "-1002:12345", wantChat: "-1002", wantTopic: 12345},
		{name: "invalid topic", arg: "-1002:abc", wantErr: true},
		{name: "empty topic", arg: "-1002:", wantErr: true},
		{name: "zero topic", arg: "-1002:0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTargetWithTopic(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ChatIDArg != tt.wantChat || got.TopicID != tt.wantTopic {
				t.Fatalf("got %+v want chat=%q topic=%d", got, tt.wantChat, tt.wantTopic)
			}
		})
	}
}

func TestFormatWatchTargetDisplay(t *testing.T) {
	if got := formatWatchTargetDisplay("目标群", ""); got != "目标群" {
		t.Fatalf("got %q", got)
	}
	if got := formatWatchTargetDisplay("目标群", "VIP"); got != "目标群#VIP" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchFilterMatches(t *testing.T) {
	if !watchFilterMatches("", "anything") {
		t.Fatal("empty filter should match")
	}
	if !watchFilterMatches("msgre:hello", "say hello") {
		t.Fatal("expected match")
	}
	if watchFilterMatches("msgre:hello", "") {
		t.Fatal("empty text should not match hello")
	}
	if watchFilterMatches("msgre:hello", "world") {
		t.Fatal("expected no match")
	}
}

func TestFormatWatchListLine(t *testing.T) {
	got := formatWatchListLine(3, "源频道", "目标群#VIP", "msgre:plana|planb")
	want := "[3] 源频道 -> 目标群#VIP msgre:plana|planb"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got0 := formatWatchListLine(1, "源频道", "本地", "")
	want0 := "[1] 源频道 -> 本地"
	if got0 != want0 {
		t.Fatalf("got %q want %q", got0, want0)
	}
}

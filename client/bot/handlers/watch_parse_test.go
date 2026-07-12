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
		{name: "source+filter", args: []string{"-1001", "msgre:.*a.*"}, wantSource: "-1001", wantFilter: "msgre:.*a.*", targetIsFilter: true, omitTarget: true},
		{name: "source+target", args: []string{"-1001", "-1002"}, wantSource: "-1001", wantTarget: "-1002"},
		{name: "source+zero+filter", args: []string{"-1001", "0", "msgre:.*a.*"}, wantSource: "-1001", wantTarget: "0", wantFilter: "msgre:.*a.*"},
		{name: "source+target+filter", args: []string{"-1001", "-1002", "msgre:.*a.*"}, wantSource: "-1001", wantTarget: "-1002", wantFilter: "msgre:.*a.*"},
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

func TestFormatWatchListLine(t *testing.T) {
	got := formatWatchListLine(3, -1002229835658, -1003333444555, "msgre:.*plana.*")
	want := "[3] -1002229835658 -1003333444555 msgre:.*plana.*"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got0 := formatWatchListLine(1, -1001, 0, "")
	want0 := "[1] -1001 0"
	if got0 != want0 {
		t.Fatalf("got %q want %q", got0, want0)
	}
}

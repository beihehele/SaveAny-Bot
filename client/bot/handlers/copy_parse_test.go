package handlers

import "testing"

func TestParseCopyArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSource string
		wantTarget string
		wantFilter string
		wantCount  int
		wantErr    bool
	}{
		{name: "source+target", args: []string{"-1001", "-1002"}, wantSource: "-1001", wantTarget: "-1002", wantCount: 500},
		{name: "with topic", args: []string{"-1001", "-1002:12345"}, wantSource: "-1001", wantTarget: "-1002:12345", wantCount: 500},
		{name: "filter+count", args: []string{"-1001", "-1002", "msgre:.*a.*", "100"}, wantSource: "-1001", wantTarget: "-1002", wantFilter: "msgre:.*a.*", wantCount: 100},
		{name: "count only", args: []string{"-1001", "-1002", "50"}, wantSource: "-1001", wantTarget: "-1002", wantCount: 50},
		{name: "filter only", args: []string{"-1001", "-1002", "msgre:.*x.*"}, wantSource: "-1001", wantTarget: "-1002", wantFilter: "msgre:.*x.*", wantCount: 500},
		{name: "missing target", args: []string{"-1001"}, wantErr: true},
		{name: "target zero", args: []string{"-1001", "0"}, wantErr: true},
		{name: "empty", args: nil, wantErr: true},
		{name: "bad trailing", args: []string{"-1001", "-1002", "nope"}, wantErr: true},
		{name: "count zero", args: []string{"-1001", "-1002", "0"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCopyArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.SourceArg != tt.wantSource || got.TargetArg != tt.wantTarget {
				t.Fatalf("source/target got %q %q", got.SourceArg, got.TargetArg)
			}
			if got.FilterArg != tt.wantFilter {
				t.Fatalf("filter=%q want %q", got.FilterArg, tt.wantFilter)
			}
			if got.Count != tt.wantCount {
				t.Fatalf("count=%d want %d", got.Count, tt.wantCount)
			}
		})
	}
}

package handlers

import "testing"

func TestUpdateCallbackRequiresExactAssetApproval(t *testing.T) {
	for _, tc := range []struct {
		data string
		want int64
	}{
		{"update:42", 42}, {"update:9223372036854775807", 9223372036854775807},
		{"update", 0}, {"update:", 0}, {"update:0", 0}, {"update:-1", 0}, {"update:+42", 0},
		{"update:042", 0}, {"update:42:43", 0}, {"update:9223372036854775808", 0}, {"other:42", 0},
	} {
		t.Run(tc.data, func(t *testing.T) {
			id, err := parseUpdateAssetID([]byte(tc.data))
			if (err == nil) != (tc.want != 0) || id != tc.want {
				t.Fatalf("id=%d err=%v", id, err)
			}
		})
	}
}

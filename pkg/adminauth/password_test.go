package adminauth

import (
	"strings"
	"testing"
)

func TestPasswordHashAndValidation(t *testing.T) {
	password := []byte("isolated-test-password")
	encoded, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Hash(password)
	if err != nil || encoded == other || strings.Contains(encoded, string(password)) {
		t.Fatal("hash must be independently salted and not contain the password")
	}
	verifier, err := Parse(encoded)
	if err != nil || !verifier.Verify(password) || verifier.Verify([]byte("wrong-password")) || verifier.Verify(make([]byte, 1025)) {
		t.Fatal("password verification failed", err)
	}
	for _, input := range []string{"", "short", strings.Repeat("x", 1025)} {
		if _, err := Hash([]byte(input)); err == nil {
			t.Fatal("accepted invalid password length")
		}
	}
	for _, input := range []string{"", encoded + "$extra", strings.Replace(encoded, "m=19456", "m=999999999", 1), strings.Replace(encoded, "v=19", "v=16", 1), hashPrefix + "AA$AA"} {
		if _, err := Parse(input); err == nil {
			t.Fatal("accepted invalid or unbounded hash")
		}
	}
}

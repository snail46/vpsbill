package app

import "testing"

func TestValidRootPassword(t *testing.T) {
	for _, value := range []string{"Password1", "a2345678", "1234567Z"} {
		if !validRootPassword(value) {
			t.Fatalf("expected %q to be valid", value)
		}
	}
	for _, value := range []string{"short1", "onlyletters", "12345678", ""} {
		if validRootPassword(value) {
			t.Fatalf("expected %q to be invalid", value)
		}
	}
}

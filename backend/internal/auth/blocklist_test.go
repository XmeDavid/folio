package auth

import "testing"

func TestIsCommonPassword_blocksKnown(t *testing.T) {
	for _, p := range []string{"password", "123456", "qwerty", "admin"} {
		if !IsCommonPassword(p) {
			t.Errorf("%q should be flagged as common", p)
		}
	}
}

func TestIsCommonPassword_allowsUnusual(t *testing.T) {
	for _, p := range []string{"correct horse battery staple", "p4SsW0rd!wPzXq"} {
		if IsCommonPassword(p) {
			t.Errorf("%q should not be flagged", p)
		}
	}
}

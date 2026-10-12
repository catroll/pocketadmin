package identity_test

import (
	"testing"

	"pocketadmin/internal/identity"
)

// TestNormalisePhone covers the shapes people actually type.
//
// Uniqueness compares stored values, so two spellings of one number must
// normalise to the same string or the constraint is defeated by formatting.
func TestNormalisePhone(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"+86 138-0000-1234", "+8613800001234"},
		{"+8613800001234", "+8613800001234"},
		{"+86 (138) 0000 1234", "+8613800001234"},
		{"13800001234", "13800001234"},
		{"138 0000 1234", "13800001234"},
		{"", ""},
		{"   ", ""},
		{"not a number", ""},
		{"+", "+"},
	}

	for _, tc := range cases {
		if got := identity.NormalisePhone(tc.in); got != tc.want {
			t.Errorf("NormalisePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalisePhoneIsIdempotent: normalising an already-normalised value must
// not change it, or repeated saves would keep rewriting the row.
func TestNormalisePhoneIsIdempotent(t *testing.T) {
	inputs := []string{"+86 138-0000-1234", "13800001234", "", "not a number"}

	for _, in := range inputs {
		once := identity.NormalisePhone(in)
		if twice := identity.NormalisePhone(once); twice != once {
			t.Errorf("normalising %q twice changed it: %q then %q", in, once, twice)
		}
	}
}

// TestNormalisePhoneKeepsOnlyALeadingPlus: a plus that is not at the start is a
// separator artefact, not part of the number.
func TestNormalisePhoneKeepsOnlyALeadingPlus(t *testing.T) {
	if got := identity.NormalisePhone("86+13800001234"); got != "8613800001234" {
		t.Errorf("a mid-string plus should be dropped, got %q", got)
	}
}

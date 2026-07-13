package admin

import "testing"

func TestConstantTimeAdminKeyEq(t *testing.T) {
	if !constantTimeAdminKeyEq("secret-admin", "secret-admin") {
		t.Fatal("equal keys should match")
	}
	if constantTimeAdminKeyEq("secret-admin", "secret-admix") {
		t.Fatal("different keys must not match")
	}
	if constantTimeAdminKeyEq("", "x") {
		t.Fatal("empty want must fail")
	}
	if constantTimeAdminKeyEq("x", "") {
		t.Fatal("empty want must fail")
	}
}

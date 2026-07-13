package lease

import (
	"strings"
	"testing"
)

func TestRedactProxyURL(t *testing.T) {
	got := redactProxyURL("http://user:pass@proxy.example:8080")
	if got == "" || got == "http://user:pass@proxy.example:8080" {
		t.Fatalf("expected redacted url, got %q", got)
	}
	if strings.Contains(got, "pass") {
		t.Fatalf("password leaked: %q", got)
	}
	l := Lease{AccountID: "a", ProxyURL: "http://user:pass@h:1", Attempt: 1}
	s := l.String()
	if strings.Contains(s, "pass") {
		t.Fatalf("Lease.String leaked proxy password: %s", s)
	}
}

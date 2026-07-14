package authimport

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func b64u(v any) string {
	b, _ := json.Marshal(v)
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func TestEmailFromJWTOnToAccount(t *testing.T) {
	at := b64u(map[string]string{"alg": "none"}) + "." + b64u(map[string]string{"email": "jwtuser@x.ai", "sub": "u1"}) + ".x"
	c := ImportedCredential{SourceKey: "j1", AccessToken: at, RefreshToken: "rt"}
	a, err := ToAccount(c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "jwtuser@x.ai" {
		t.Fatalf("email=%q rawFrom=%q", a.Email, emailFromAccessToken(at))
	}
}

func TestParseJSONFillsEmail(t *testing.T) {
	at := b64u(map[string]string{"alg": "none"}) + "." + b64u(map[string]string{"email": "jwt3@x.ai", "sub": "u-jwt-3"}) + ".sig"
	raw, _ := json.Marshal([]map[string]string{{
		"key": "jwt3", "access_token": at, "refresh_token": "rt-jwt-unique-3",
	}})
	creds, _, err := ParseGrokAuthJSONDetailed(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("n=%d", len(creds))
	}
	t.Logf("access=%q emailFrom=%q", creds[0].AccessToken, emailFromAccessToken(creds[0].AccessToken))
	a, err := ToAccount(creds[0], time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "jwt3@x.ai" {
		t.Fatalf("email=%q cred=%q access=%q helper=%q", a.Email, creds[0].Email, creds[0].AccessToken, emailFromAccessToken(at))
	}
}

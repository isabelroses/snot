package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := sign("s3cr3t", body)

	if !verifySignature("s3cr3t", body, good) {
		t.Error("valid signature rejected")
	}
	if verifySignature("s3cr3t", body, sign("wrong", body)) {
		t.Error("signature from wrong secret accepted")
	}
	if verifySignature("s3cr3t", body, "not-hex") {
		t.Error("non-hex signature accepted")
	}
	if verifySignature("s3cr3t", body, "") {
		t.Error("empty signature accepted")
	}
}

func TestParsePush(t *testing.T) {
	body := []byte(`{
		"ref": "refs/heads/main",
		"before": "1111111111111111111111111111111111111111",
		"after": "2222222222222222222222222222222222222222",
		"repository": {"name": "Demo", "owner": {"username": "Isabel"}},
		"pusher": {"username": "Isabel"}
	}`)
	p, err := parsePush(body)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref != "refs/heads/main" || p.After != "2222222222222222222222222222222222222222" {
		t.Errorf("ref/after = %q %q", p.Ref, p.After)
	}
	if p.owner() != "Isabel" || p.name() != "Demo" || p.pusher() != "Isabel" {
		t.Errorf("owner/name/pusher = %q %q %q", p.owner(), p.name(), p.pusher())
	}
}

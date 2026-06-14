// Package webhook receives Forgejo push webhooks and emits
// sh.tangled.git.refUpdate events into snot's knot eventstream.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// verifySignature checks a Forgejo X-Gitea-Signature (hex HMAC-SHA256 of the
// raw body keyed by the shared secret) in constant time.
func verifySignature(secret string, body []byte, sigHex string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	got, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	return hmac.Equal(expected, got)
}

type pushPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Username string `json:"username"`
			Login    string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Pusher struct {
		Username string `json:"username"`
		Login    string `json:"login"`
	} `json:"pusher"`
}

func parsePush(body []byte) (*pushPayload, error) {
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// owner/name/pusher prefer Forgejo's `username`, falling back to the
// deprecated `login` alias.
func (p *pushPayload) owner() string {
	if p.Repository.Owner.Username != "" {
		return p.Repository.Owner.Username
	}
	return p.Repository.Owner.Login
}

func (p *pushPayload) name() string { return p.Repository.Name }

func (p *pushPayload) pusher() string {
	if p.Pusher.Username != "" {
		return p.Pusher.Username
	}
	return p.Pusher.Login
}

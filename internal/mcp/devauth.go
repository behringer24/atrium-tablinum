package mcpserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

// DevVerifier accepts one static bearer token and maps it to a fixed local account. It
// exists for the MCP Inspector and local tests and must only be used with dev_mode.
func DevVerifier(store *users.Store, token, email string) auth.TokenVerifier {
	var (
		once sync.Once
		id   string
		err  error
	)
	return func(ctx context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		once.Do(func() {
			var u users.User
			u, err = store.Provision(ctx, users.Identity{IDP: "dev", Subject: email, Email: email, DisplayName: "Dev"})
			id = u.ID.String()
		})
		if err != nil {
			return nil, err
		}
		return &auth.TokenInfo{UserID: id, Expiration: time.Now().Add(time.Hour)}, nil
	}
}

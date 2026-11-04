package oauth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// errTokenCheck is what an unauthenticated caller sees when the check itself failed (for
// example the database is down): the SDK writes the text of such an error into the 500
// response, so it must not carry internals.
var errTokenCheck = errors.New("token check failed")

// Verifier checks bearer tokens on the MCP endpoint: the token must be a live access
// token issued for this resource (audience binding, RFC 8707) and its user must be
// active. Anything else is a 401; a failing check is logged and answered without details.
func (s *Server) Verifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		info, err := s.store.LookupAccess(ctx, token)
		if errors.Is(err, ErrNotFound) {
			return nil, auth.ErrInvalidToken
		}
		if err != nil {
			slog.Error("Token lookup failed", "error", err)
			return nil, errTokenCheck
		}
		if info.Resource != s.resource {
			return nil, auth.ErrInvalidToken
		}
		u, err := s.users.Get(ctx, info.UserID)
		if err != nil {
			slog.Error("User lookup failed", "error", err)
			return nil, errTokenCheck
		}
		if !u.Active {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: info.UserID.String(), Expiration: info.Expires}, nil
	}
}

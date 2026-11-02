package oauth

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// Verifier checks bearer tokens on the MCP endpoint: the token must be a live access
// token issued for this resource (audience binding, RFC 8707) and its user must be
// active. Anything else is a 401.
func (s *Server) Verifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		info, err := s.store.LookupAccess(ctx, token)
		if errors.Is(err, ErrNotFound) {
			return nil, auth.ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		if info.Resource != s.resource {
			return nil, auth.ErrInvalidToken
		}
		u, err := s.users.Get(ctx, info.UserID)
		if err != nil || !u.Active {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: info.UserID.String(), Expiration: info.Expires}, nil
	}
}

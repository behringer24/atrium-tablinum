package users

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

func mapNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ErrEmailTaken means the e-mail belongs to an account that was created through another
// login (provider or subject) and linking by e-mail is not enabled.
var ErrEmailTaken = errors.New("e-mail belongs to a different login")

// ErrUserLimit means that creating one more account would exceed the number of users the instance
// may have. Existing accounts are never affected.
var ErrUserLimit = errors.New("the number of users this instance may have is reached")

// ErrEmailUnverified means the identity provider does not vouch for the e-mail address, and the
// address is one that carries privileges or belongs to an existing account: linking it to
// that account, or creating the account of a bootstrap admin, needs a verified address.
var ErrEmailUnverified = errors.New("e-mail address not verified by the identity provider")

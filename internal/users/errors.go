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
var ErrEmailTaken = errors.New("e-mail gehört zu einem anderen login")

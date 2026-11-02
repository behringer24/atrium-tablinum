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

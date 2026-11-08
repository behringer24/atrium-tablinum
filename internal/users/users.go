// Package users manages accounts (just-in-time provisioning, status, instance admins)
// and local groups.
package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/audit"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDisabled  = errors.New("user is disabled")
	ErrBootstrap = errors.New("bootstrap admins are defined in the configuration and cannot be changed")
)

type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	IDP         string
	Subject     string
	Active      bool
	// InstanceAdmin is the effective flag: stored flag OR listed in bootstrap_admins.
	InstanceAdmin   bool
	DefaultSpaceID  *uuid.UUID
	PersonalSpaceID *uuid.UUID // nil when personal spaces are disabled
	LastLoginAt     *time.Time
	DisabledAt      *time.Time // set while the account is disabled (offboarding)
	CreatedAt       time.Time
}

// Identity is what a verified login hands over.
type Identity struct {
	IDP         string
	Subject     string
	Email       string
	DisplayName string
	// EmailVerified says that the identity provider vouches for the address (see idp.Identity).
	// Only then may it be linked to an existing account or create a bootstrap admin.
	EmailVerified bool
}

type Options struct {
	BootstrapAdmins []string
	PersonalEnabled bool
	// LinkByEmail lets a login through another provider (or with another subject) take over
	// the existing account with the same e-mail. Off by default: who can issue an address
	// in the allowed domain at a second provider would otherwise become that account,
	// including an instance admin from bootstrap_admins.
	LinkByEmail bool
	// MaxUsers returns the number of active users the instance may have; nil or 0 = unlimited. It is
	// asked when a new account would be created, so a change applies at once.
	// Instance admins from bootstrap_admins are exempt, so that a limit can never lock them out.
	MaxUsers func() int
}

type Store struct {
	pool      *pgxpool.Pool
	bootstrap map[string]bool
	personal  bool
	linkEmail bool
	maxUsers  func() int

	// OnChange is called after changes that can alter effective roles (status, admin
	// flag, group membership). Wire it to authz.Checker.InvalidateAll.
	OnChange func()
}

func New(pool *pgxpool.Pool, opt Options) *Store {
	b := map[string]bool{}
	for _, e := range opt.BootstrapAdmins {
		b[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return &Store{pool: pool, bootstrap: b, personal: opt.PersonalEnabled, linkEmail: opt.LinkByEmail, maxUsers: opt.MaxUsers}
}

func (s *Store) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

const userSelect = `
SELECT u.id, u.email, u.display_name, u.idp, u.idp_subject, u.status = 'active', u.instance_admin,
       u.default_space_id, u.last_login_at, u.disabled_at, u.created_at,
       (SELECT sp.id FROM spaces sp WHERE sp.owner_user_id = u.id AND sp.type = 'personal')
FROM users u `

func (s *Store) scan(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.IDP, &u.Subject, &u.Active, &u.InstanceAdmin,
		&u.DefaultSpaceID, &u.LastLoginAt, &u.DisabledAt, &u.CreatedAt, &u.PersonalSpaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.InstanceAdmin = u.InstanceAdmin || s.bootstrap[strings.ToLower(u.Email)]
	return u, nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (User, error) {
	return s.scan(s.pool.QueryRow(ctx, userSelect+`WHERE u.id = $1`, id))
}

func (s *Store) GetByEmail(ctx context.Context, email string) (User, error) {
	return s.scan(s.pool.QueryRow(ctx, userSelect+`WHERE u.email = $1`, email))
}

// List returns all users ordered by email.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, userSelect+`ORDER BY u.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// InstanceAdminEmails lists the active accounts that are instance admins (stored flag or
// bootstrap list), ordered by e-mail.
func (s *Store) InstanceAdminEmails(ctx context.Context) ([]string, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, u := range all {
		if u.Active && u.InstanceAdmin {
			out = append(out, u.Email)
		}
	}
	return out, nil
}

// Provision finds or creates the account for a verified identity (just-in-time) and
// records the login. Lookup is by (idp, subject). Only with LinkByEmail a second lookup by
// e-mail follows, so the same person signing in through a second configured IdP lands on
// the same account (the e-mail must then be verified by the caller); without it an
// address that is already taken by another login gets ErrEmailTaken. Disabled accounts
// get ErrDisabled.
func (s *Store) Provision(ctx context.Context, id Identity) (User, error) {
	if id.IDP == "" || id.Subject == "" || id.Email == "" {
		return User{}, errors.New("identity incomplete")
	}
	uid, err := s.provision(ctx, id)
	if isUniqueViolation(err) { // concurrent first login of the same person
		uid, err = s.provision(ctx, id)
	}
	if err != nil {
		return User{}, err
	}
	u, err := s.Get(ctx, uid)
	if err != nil {
		return User{}, err
	}
	if !u.Active {
		return User{}, ErrDisabled
	}
	return u, nil
}

func (s *Store) provision(ctx context.Context, id Identity) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var (
		uid    uuid.UUID
		active bool
	)
	err = tx.QueryRow(ctx, `SELECT id, status = 'active' FROM users WHERE idp = $1 AND idp_subject = $2`,
		id.IDP, id.Subject).Scan(&uid, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		var taken bool
		err = tx.QueryRow(ctx, `SELECT id, status = 'active', true FROM users WHERE email = $1`, id.Email).Scan(&uid, &active, &taken)
		if taken && !s.linkEmail {
			return uuid.Nil, ErrEmailTaken
		}
		if taken && !id.EmailVerified {
			return uuid.Nil, ErrEmailUnverified
		}
		if taken {
			slog.Warn("Account linked by e-mail", "idp", id.IDP, "user", uid)
			if aerr := audit.Insert(ctx, tx, audit.Event{UserID: &uid, Client: "system", Action: "account_linked",
				Details: map[string]any{"idp": id.IDP, "email": id.Email}}); aerr != nil {
				return uuid.Nil, aerr
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) && s.maxUsers != nil && !s.bootstrap[strings.ToLower(id.Email)] {
		if max := s.maxUsers(); max > 0 {
			var n int
			if qerr := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE status = 'active'`).Scan(&n); qerr != nil {
				return uuid.Nil, qerr
			}
			if n >= max {
				return uuid.Nil, ErrUserLimit
			}
		}
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows) && s.bootstrap[strings.ToLower(id.Email)] && !id.EmailVerified:
		// The address of an instance admin must not be claimed with an address nobody vouches for.
		return uuid.Nil, ErrEmailUnverified
	case errors.Is(err, pgx.ErrNoRows):
		err = tx.QueryRow(ctx, `INSERT INTO users (email, display_name, idp, idp_subject, last_login_at)
			VALUES ($1, $2, $3, $4, now()) RETURNING id`, id.Email, id.DisplayName, id.IDP, id.Subject).Scan(&uid)
		if err != nil {
			return uuid.Nil, err
		}
	case err != nil:
		return uuid.Nil, err
	case !active:
		return uid, nil // caller turns this into ErrDisabled; nothing is touched
	default:
		if _, err = tx.Exec(ctx, `UPDATE users SET last_login_at = now(),
			display_name = CASE WHEN $2 <> '' THEN $2 ELSE display_name END WHERE id = $1`, uid, id.DisplayName); err != nil {
			return uuid.Nil, err
		}
	}

	if s.personal {
		name := id.DisplayName
		if name == "" {
			name = id.Email
		}
		if _, err = tx.Exec(ctx, `INSERT INTO spaces (slug, name, type, owner_user_id)
			VALUES ('personal-' || ($1::uuid)::text, $2, 'personal', $1::uuid)
			ON CONFLICT (owner_user_id) WHERE type = 'personal' DO NOTHING`, uid, name); err != nil {
			return uuid.Nil, err
		}
	}
	return uid, tx.Commit(ctx)
}

// checkRoom refuses to reactivate an account when the number of other active accounts has reached the
// user limit.
func (s *Store) checkRoom(ctx context.Context, id uuid.UUID) error {
	if s.maxUsers == nil {
		return nil
	}
	max := s.maxUsers()
	if max <= 0 {
		return nil
	}
	var email string
	var wasActive bool
	if err := s.pool.QueryRow(ctx, `SELECT email, status = 'active' FROM users WHERE id = $1`, id).Scan(&email, &wasActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if wasActive || s.bootstrap[strings.ToLower(email)] {
		return nil
	}
	n, err := s.CountActive(ctx)
	if err != nil {
		return err
	}
	if n >= max {
		return ErrUserLimit
	}
	return nil
}

// CountActive returns the number of active accounts, the number the user limit is compared with.
func (s *Store) CountActive(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE status = 'active'`).Scan(&n)
	return n, err
}

// SetActive disables (offboarding) or re-enables an account. Revoking tokens is the
// caller's job (oauth package).
func (s *Store) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	status := "active"
	if !active {
		status = "disabled"
	}
	if active {
		// A reactivated account is an active account again: it needs room under the user limit, so
		// that offboarding and reactivating cannot be used to go beyond it. Administrators from
		// bootstrap_admins are exempt, as when an account is created.
		if err := s.checkRoom(ctx, id); err != nil {
			return err
		}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET status = $2,
		disabled_at = CASE WHEN $2 = 'disabled' THEN now() ELSE NULL END WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.changed()
	return nil
}

// SetInstanceAdmin grants or revokes the stored admin flag. Bootstrap admins cannot
// be changed here.
func (s *Store) SetInstanceAdmin(ctx context.Context, id uuid.UUID, admin bool) error {
	u, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.bootstrap[strings.ToLower(u.Email)] {
		return ErrBootstrap
	}
	if _, err := s.pool.Exec(ctx, `UPDATE users SET instance_admin = $2 WHERE id = $1`, id, admin); err != nil {
		return err
	}
	s.changed()
	return nil
}

// SetDefaultSpace stores the preferred target for saving; nil clears it. The caller
// must have verified that the user may write to that space.
func (s *Store) SetDefaultSpace(ctx context.Context, userID uuid.UUID, spaceID *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET default_space_id = $2 WHERE id = $1`, userID, spaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

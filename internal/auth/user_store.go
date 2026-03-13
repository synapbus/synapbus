package auth

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,64}$`)

// UserStore defines the storage interface for user operations.
type UserStore interface {
	CreateUser(ctx context.Context, username, password, displayName string) (*User, error)
	GetUserByID(ctx context.Context, id int64) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	UpdatePassword(ctx context.Context, userID int64, newPassword string) error
	ListUsers(ctx context.Context) ([]*User, error)
	CountUsers(ctx context.Context) (int, error)
	VerifyPassword(ctx context.Context, username, password string) (*User, error)
}

// SQLiteUserStore implements UserStore using SQLite.
type SQLiteUserStore struct {
	db         *sql.DB
	bcryptCost int
}

// NewSQLiteUserStore creates a new SQLite-backed user store.
func NewSQLiteUserStore(db *sql.DB, bcryptCost int) *SQLiteUserStore {
	if bcryptCost < 10 {
		bcryptCost = 12
	}
	return &SQLiteUserStore{db: db, bcryptCost: bcryptCost}
}

// CreateUser creates a new user with a bcrypt-hashed password.
func (s *SQLiteUserStore) CreateUser(ctx context.Context, username, password, displayName string) (*User, error) {
	if !usernameRegex.MatchString(username) {
		return nil, ErrInvalidUsername
	}

	if len(password) < 8 {
		return nil, ErrPasswordTooShort
	}
	if len(password) > 72 {
		return nil, ErrPasswordTooLong
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Determine role: first user is admin
	count, err := s.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}

	role := RoleUser
	if count == 0 {
		role = RoleAdmin
	}

	if displayName == "" {
		displayName = username
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, display_name, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		username, string(hash), displayName, role,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, ErrDuplicateUsername
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get user id: %w", err)
	}

	return &User{
		ID:          id,
		Username:    username,
		DisplayName: displayName,
		Role:        role,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

// GetUserByID retrieves a user by their ID.
func (s *SQLiteUserStore) GetUserByID(ctx context.Context, id int64) (*User, error) {
	user := &User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, display_name, role, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("query user: %w", err)
	}
	return user, nil
}

// GetUserByUsername retrieves a user by their username.
func (s *SQLiteUserStore) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	user := &User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, display_name, role, created_at, updated_at
		 FROM users WHERE username = ?`, username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("query user: %w", err)
	}
	return user, nil
}

// UpdatePassword changes a user's password.
func (s *SQLiteUserStore) UpdatePassword(ctx context.Context, userID int64, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrPasswordTooShort
	}
	if len(newPassword) > 72 {
		return ErrPasswordTooLong
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(hash), userID,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ListUsers returns all users.
func (s *SQLiteUserStore) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, password_hash, display_name, role, created_at, updated_at
		 FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
			&u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	if users == nil {
		users = []*User{}
	}
	return users, rows.Err()
}

// CountUsers returns the number of registered users.
func (s *SQLiteUserStore) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

// VerifyPassword checks a username/password combination and returns the user if valid.
func (s *SQLiteUserStore) VerifyPassword(ctx context.Context, username, password string) (*User, error) {
	user, err := s.GetUserByUsername(ctx, username)
	if err != nil {
		// Do a dummy bcrypt comparison to prevent timing attacks
		bcrypt.CompareHashAndPassword(
			[]byte("$2a$12$dummyhashvaluefortimingatttack0000000000000000000"),
			[]byte(password),
		)
		return nil, ErrInvalidPassword
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidPassword
	}

	return user, nil
}

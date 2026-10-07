package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Attributes struct {
	Might   int `json:"might"`
	Reflex  int `json:"reflex"`
	Insight int `json:"insight"`
	Resolve int `json:"resolve"`
}

type Character struct {
	ID         int64
	Name       string
	Attributes Attributes
	X          float64
	Y          float64
	LastLogin  time.Time
}

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS characters (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    might INTEGER NOT NULL CHECK (might >= 0),
    reflex INTEGER NOT NULL CHECK (reflex >= 0),
    insight INTEGER NOT NULL CHECK (insight >= 0),
    resolve INTEGER NOT NULL CHECK (resolve >= 0),
    x REAL NOT NULL DEFAULT 0,
    y REAL NOT NULL DEFAULT 0,
    last_login TEXT NOT NULL
);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create character schema: %w", err)
	}
	return nil
}

func (s *Store) CreateCharacter(ctx context.Context, character Character) (Character, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO characters (name, might, reflex, insight, resolve, x, y, last_login)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		character.Name,
		character.Attributes.Might,
		character.Attributes.Reflex,
		character.Attributes.Insight,
		character.Attributes.Resolve,
		character.X,
		character.Y,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return Character{}, fmt.Errorf("create character: %w", err)
	}

	character.ID, err = result.LastInsertId()
	if err != nil {
		return Character{}, fmt.Errorf("read created character id: %w", err)
	}
	character.LastLogin = now
	return character, nil
}

func (s *Store) LoadCharacter(ctx context.Context, name string) (Character, error) {
	var character Character
	var lastLogin string
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, might, reflex, insight, resolve, x, y, last_login
FROM characters
WHERE name = ?`, name).Scan(
		&character.ID,
		&character.Name,
		&character.Attributes.Might,
		&character.Attributes.Reflex,
		&character.Attributes.Insight,
		&character.Attributes.Resolve,
		&character.X,
		&character.Y,
		&lastLogin,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Character{}, fmt.Errorf("load character %q: %w", name, err)
		}
		return Character{}, fmt.Errorf("load character: %w", err)
	}

	character.LastLogin, err = time.Parse(time.RFC3339Nano, lastLogin)
	if err != nil {
		return Character{}, fmt.Errorf("parse character last login: %w", err)
	}

	character.LastLogin = time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		"UPDATE characters SET last_login = ? WHERE id = ?",
		character.LastLogin.Format(time.RFC3339Nano),
		character.ID,
	); err != nil {
		return Character{}, fmt.Errorf("update character last login: %w", err)
	}
	return character, nil
}

func (s *Store) SaveCharacter(ctx context.Context, character Character) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE characters
SET might = ?, reflex = ?, insight = ?, resolve = ?, x = ?, y = ?, last_login = ?
WHERE id = ?`,
		character.Attributes.Might,
		character.Attributes.Reflex,
		character.Attributes.Insight,
		character.Attributes.Resolve,
		character.X,
		character.Y,
		character.LastLogin.UTC().Format(time.RFC3339Nano),
		character.ID,
	)
	if err != nil {
		return fmt.Errorf("save character: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check saved character: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("save character %d: %w", character.ID, sql.ErrNoRows)
	}
	return nil
}

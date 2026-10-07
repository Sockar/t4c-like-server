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
	Power     int `json:"power"`
	Agility   int `json:"agility"`
	Endurance int `json:"endurance"`
	Insight   int `json:"insight"`
}

type Character struct {
	ID         int64
	Name       string
	Attributes Attributes
	X          float64
	Y          float64
	HP         int
	MP         int
	XP         int64
	LastLogin  time.Time
}

type QuestProgress struct {
	CharacterID int64
	QuestID     string
	Status      string
	Progress    int
	UpdatedAt   time.Time
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
    power INTEGER NOT NULL CHECK (power >= 0),
    agility INTEGER NOT NULL CHECK (agility >= 0),
    endurance INTEGER NOT NULL CHECK (endurance >= 0),
    insight INTEGER NOT NULL CHECK (insight >= 0),
    xp INTEGER NOT NULL DEFAULT 0 CHECK (xp >= 0),
    mp INTEGER NOT NULL DEFAULT 20 CHECK (mp >= 0),
    hp INTEGER NOT NULL DEFAULT 100 CHECK (hp >= 0),
    x REAL NOT NULL DEFAULT 0,
    y REAL NOT NULL DEFAULT 0,
    last_login TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS character_quests (
    character_id INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    quest_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('accepted', 'ready', 'turned_in')),
    progress INTEGER NOT NULL CHECK (progress >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (character_id, quest_id)
);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create persistence schema: %w", err)
	}

	columns, err := s.characterColumns(ctx)
	if err != nil {
		return err
	}
	for _, rename := range [][2]string{
		{"might", "power"},
		{"reflex", "agility"},
		{"resolve", "endurance"},
	} {
		if columns[rename[0]] && !columns[rename[1]] {
			if _, err := s.db.ExecContext(ctx,
				fmt.Sprintf("ALTER TABLE characters RENAME COLUMN %s TO %s", rename[0], rename[1]),
			); err != nil {
				return fmt.Errorf("migrate character column %s: %w", rename[0], err)
			}
			delete(columns, rename[0])
			columns[rename[1]] = true
		}
	}
	for column, definition := range map[string]string{
		"xp": "INTEGER NOT NULL DEFAULT 0 CHECK (xp >= 0)",
		"mp": "INTEGER NOT NULL DEFAULT 20 CHECK (mp >= 0)",
		"hp": "INTEGER NOT NULL DEFAULT 100 CHECK (hp >= 0)",
	} {
		if !columns[column] {
			if _, err := s.db.ExecContext(ctx,
				fmt.Sprintf("ALTER TABLE characters ADD COLUMN %s %s", column, definition),
			); err != nil {
				return fmt.Errorf("add character column %s: %w", column, err)
			}
		}
	}
	return nil
}

func (s *Store) characterColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(characters)")
	if err != nil {
		return nil, fmt.Errorf("inspect character schema: %w", err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("read character schema: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate character schema: %w", err)
	}
	return columns, nil
}

func (s *Store) CreateCharacter(ctx context.Context, character Character) (Character, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO characters (name, power, agility, endurance, insight, xp, mp, hp, x, y, last_login)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		character.Name,
		character.Attributes.Power,
		character.Attributes.Agility,
		character.Attributes.Endurance,
		character.Attributes.Insight,
		character.XP,
		character.MP,
		character.HP,
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
SELECT id, name, power, agility, endurance, insight, xp, mp, hp, x, y, last_login
FROM characters
WHERE name = ?`, name).Scan(
		&character.ID,
		&character.Name,
		&character.Attributes.Power,
		&character.Attributes.Agility,
		&character.Attributes.Endurance,
		&character.Attributes.Insight,
		&character.XP,
		&character.MP,
		&character.HP,
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
SET power = ?, agility = ?, endurance = ?, insight = ?, xp = ?, mp = ?, hp = ?, x = ?, y = ?, last_login = ?
WHERE id = ?`,
		character.Attributes.Power,
		character.Attributes.Agility,
		character.Attributes.Endurance,
		character.Attributes.Insight,
		character.XP,
		character.MP,
		character.HP,
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

func (s *Store) CreateQuestProgress(ctx context.Context, progress QuestProgress) error {
	progress.UpdatedAt = time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO character_quests (character_id, quest_id, status, progress, updated_at)
VALUES (?, ?, ?, ?, ?)`,
		progress.CharacterID,
		progress.QuestID,
		progress.Status,
		progress.Progress,
		progress.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("create quest progress: %w", err)
	}
	return nil
}

func (s *Store) LoadQuestProgress(ctx context.Context, characterID int64) ([]QuestProgress, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT character_id, quest_id, status, progress, updated_at
FROM character_quests
WHERE character_id = ?
ORDER BY quest_id`, characterID)
	if err != nil {
		return nil, fmt.Errorf("load quest progress: %w", err)
	}
	defer rows.Close()

	var progress []QuestProgress
	for rows.Next() {
		var item QuestProgress
		var updatedAt string
		if err := rows.Scan(&item.CharacterID, &item.QuestID, &item.Status, &item.Progress, &updatedAt); err != nil {
			return nil, fmt.Errorf("read quest progress: %w", err)
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse quest progress timestamp: %w", err)
		}
		progress = append(progress, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quest progress: %w", err)
	}
	return progress, nil
}

func (s *Store) SaveCharacterAndQuests(ctx context.Context, character Character, quests []QuestProgress) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin character and quest update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
UPDATE characters
SET power = ?, agility = ?, endurance = ?, insight = ?, xp = ?, mp = ?, hp = ?, x = ?, y = ?, last_login = ?
WHERE id = ?`,
		character.Attributes.Power,
		character.Attributes.Agility,
		character.Attributes.Endurance,
		character.Attributes.Insight,
		character.XP,
		character.MP,
		character.HP,
		character.X,
		character.Y,
		character.LastLogin.UTC().Format(time.RFC3339Nano),
		character.ID,
	)
	if err != nil {
		return fmt.Errorf("save character with quest progress: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check saved character with quest progress: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("save character %d with quest progress: %w", character.ID, sql.ErrNoRows)
	}
	for _, quest := range quests {
		if err := upsertQuestProgress(ctx, tx, quest); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit character and quest update: %w", err)
	}
	return nil
}

func upsertQuestProgress(ctx context.Context, tx *sql.Tx, progress QuestProgress) error {
	progress.UpdatedAt = time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO character_quests (character_id, quest_id, status, progress, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(character_id, quest_id) DO UPDATE SET
	status = excluded.status,
	progress = excluded.progress,
	updated_at = excluded.updated_at`,
		progress.CharacterID,
		progress.QuestID,
		progress.Status,
		progress.Progress,
		progress.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("save quest progress %q: %w", progress.QuestID, err)
	}
	return nil
}

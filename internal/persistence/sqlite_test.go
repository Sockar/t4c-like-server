package persistence

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateLoadAndSaveCharacter(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	created, err := store.CreateCharacter(ctx, Character{
		Name: "Aster",
		Attributes: Attributes{
			Power: 3, Agility: 4, Insight: 2, Endurance: 5,
		},
		X: 10, Y: 12, HP: 50, MP: 14, XP: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.LastLogin.IsZero() {
		t.Fatalf("created character is missing generated fields: %+v", created)
	}

	loaded, err := store.LoadCharacter(ctx, "aster")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != created.ID || loaded.Name != "Aster" || loaded.Attributes != created.Attributes {
		t.Fatalf("loaded character does not match creation: got %+v, want %+v", loaded, created)
	}
	if loaded.X != 10 || loaded.Y != 12 {
		t.Fatalf("unexpected loaded position: (%v, %v)", loaded.X, loaded.Y)
	}
	if loaded.HP != 50 || loaded.MP != 14 || loaded.XP != 7 {
		t.Fatalf("unexpected character resources: hp=%d mp=%d xp=%d", loaded.HP, loaded.MP, loaded.XP)
	}

	loaded.X, loaded.Y = 24, 31
	loaded.HP, loaded.MP, loaded.XP = 45, 10, 15
	loaded.LastLogin = time.Now().UTC()
	if err := store.SaveCharacter(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.LoadCharacter(ctx, loaded.Name)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.X != 24 || reloaded.Y != 31 {
		t.Fatalf("saved position was not persisted: (%v, %v)", reloaded.X, reloaded.Y)
	}
	if reloaded.HP != 45 || reloaded.MP != 10 || reloaded.XP != 15 {
		t.Fatalf("saved resources were not persisted: hp=%d mp=%d xp=%d", reloaded.HP, reloaded.MP, reloaded.XP)
	}
}

func TestCreateCharacterRejectsDuplicateName(t *testing.T) {
	store, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.CreateCharacter(context.Background(), Character{Name: "Aster"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCharacter(context.Background(), Character{Name: "aster"}); err == nil {
		t.Fatal("expected duplicate case-insensitive name to fail")
	}
}

func TestLegacyCharacterSchemaMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE characters (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	might INTEGER NOT NULL,
	reflex INTEGER NOT NULL,
	insight INTEGER NOT NULL,
	resolve INTEGER NOT NULL,
	x REAL NOT NULL DEFAULT 0,
	y REAL NOT NULL DEFAULT 0,
	last_login TEXT NOT NULL
);
INSERT INTO characters (id, name, might, reflex, insight, resolve, x, y, last_login)
VALUES (1, 'Aster', 7, 4, 5, 4, 10, 11, '2026-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	character, err := store.LoadCharacter(context.Background(), "Aster")
	if err != nil {
		t.Fatal(err)
	}
	if character.Attributes != (Attributes{Power: 7, Agility: 4, Endurance: 4, Insight: 5}) {
		t.Fatalf("legacy attributes were not mapped: %+v", character.Attributes)
	}
	if character.XP != 0 || character.MP != 20 || character.HP != 100 {
		t.Fatalf("unexpected defaults for migrated fields: %+v", character)
	}
}

func TestQuestProgressPersistence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	character, err := store.CreateCharacter(ctx, Character{
		Name: "Aster", Attributes: Attributes{Power: 5, Agility: 5, Endurance: 5, Insight: 5},
		HP: 45, MP: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	progress := QuestProgress{CharacterID: character.ID, QuestID: "fen_patrol", Status: "accepted"}
	if err := store.CreateQuestProgress(ctx, progress); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadQuestProgress(ctx, character.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Status != "accepted" || loaded[0].Progress != 0 {
		t.Fatalf("unexpected quest state: %+v", loaded)
	}

	character.XP = 25
	loaded[0].Status = "ready"
	loaded[0].Progress = 2
	if err := store.SaveCharacterAndQuests(ctx, character, loaded); err != nil {
		t.Fatal(err)
	}
	reloadedCharacter, err := store.LoadCharacter(ctx, character.Name)
	if err != nil {
		t.Fatal(err)
	}
	reloadedQuest, err := store.LoadQuestProgress(ctx, character.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedCharacter.XP != 25 || len(reloadedQuest) != 1 ||
		reloadedQuest[0].Status != "ready" || reloadedQuest[0].Progress != 2 {
		t.Fatalf("atomic character/quest update was not persisted: character=%+v quests=%+v", reloadedCharacter, reloadedQuest)
	}
}

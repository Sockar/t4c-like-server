package persistence

import (
	"context"
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
			Might: 3, Reflex: 4, Insight: 2, Resolve: 5,
		},
		X: 10, Y: 12,
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

	loaded.X, loaded.Y = 24, 31
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

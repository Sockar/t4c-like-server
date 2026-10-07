package network

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sockar/t4c-like-server/internal/game"
	"github.com/Sockar/t4c-like-server/internal/persistence"
	"github.com/Sockar/t4c-like-server/internal/protocol"
	"github.com/gorilla/websocket"
)

func TestWebSocketCharacterCreationAndPositionUpdate(t *testing.T) {
	store, err := persistence.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	world := game.NewWorld(store, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	worldDone := make(chan struct{})
	go func() {
		defer close(worldDone)
		world.Run(ctx)
	}()

	websocketServer := NewServer(world)
	httpServer := httptest.NewServer(websocketServer)
	connectionURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	connection, _, err := websocket.DefaultDialer.Dial(connectionURL, nil)
	if err != nil {
		httpServer.Close()
		websocketServer.Close()
		cancel()
		<-worldDone
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		websocketServer.Close()
		httpServer.Close()
		cancel()
		<-worldDone
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := connection.WriteJSON(protocol.Message{
		Type: "character.create",
		Payload: map[string]any{
			"name": "Aster",
			"attributes": map[string]int{
				"power": 5, "agility": 5, "endurance": 5, "insight": 5,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var response protocol.IncomingMessage
	if err := connection.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "character.created" {
		t.Fatalf("got response %q, want character.created", response.Type)
	}

	if err := connection.WriteJSON(protocol.Message{
		Type: "position.update", Payload: map[string]float64{"x": 42, "y": -6},
	}); err != nil {
		t.Fatal(err)
	}
	for {
		if err := connection.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "world.delta" {
			continue
		}
		var delta struct {
			Players []struct {
				Name string  `json:"name"`
				X    float64 `json:"x"`
				Y    float64 `json:"y"`
			} `json:"players"`
		}
		if err := json.Unmarshal(response.Payload, &delta); err != nil {
			t.Fatal(err)
		}
		for _, player := range delta.Players {
			if player.Name == "Aster" && player.X == 42 && player.Y == -6 {
				saved, err := store.LoadCharacter(context.Background(), "Aster")
				if err != nil {
					t.Fatal(err)
				}
				if saved.X != 42 || saved.Y != -6 {
					t.Fatalf("position was not persisted: (%v, %v)", saved.X, saved.Y)
				}
				return
			}
		}
	}
}

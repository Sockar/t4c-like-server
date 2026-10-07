package game

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sockar/t4c-like-server/internal/persistence"
	"github.com/Sockar/t4c-like-server/internal/protocol"
)

type testClient struct {
	key      string
	messages []protocol.Message
}

func (c *testClient) Key() string { return c.key }

func (c *testClient) Send(message protocol.Message) bool {
	c.messages = append(c.messages, message)
	return true
}

func TestWorldCreateMoveAndBroadcastChat(t *testing.T) {
	store, err := persistence.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	world := NewWorld(store, 100*time.Millisecond)
	client := &testClient{key: "client-1"}
	world.handleEvent(context.Background(), Event{
		Client: client,
		Type:   "character.create",
		Payload: json.RawMessage(`{
			"name":"Aster",
			"attributes":{"might":5,"reflex":5,"insight":5,"resolve":5}
		}`),
	})
	if got := client.messages[len(client.messages)-1].Type; got != "character.created" {
		t.Fatalf("got response %q, want character.created", got)
	}

	world.handleEvent(context.Background(), Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":12.5,"y":-4}`),
	})
	world.broadcastDelta()
	if got := client.messages[len(client.messages)-1].Type; got != "world.delta" {
		t.Fatalf("got response %q, want world.delta", got)
	}

	world.handleEvent(context.Background(), Event{
		Client: client, Type: "chat.send",
		Payload: json.RawMessage(`{"message":"  hello world  "}`),
	})
	last := client.messages[len(client.messages)-1]
	if last.Type != "chat.message" {
		t.Fatalf("got response %q, want chat.message", last.Type)
	}
	payload, ok := last.Payload.(map[string]string)
	if !ok || payload["from"] != "Aster" || payload["message"] != "hello world" {
		t.Fatalf("unexpected chat payload: %#v", last.Payload)
	}

	saved, err := store.LoadCharacter(context.Background(), "Aster")
	if err != nil {
		t.Fatal(err)
	}
	if saved.X != 12.5 || saved.Y != -4 {
		t.Fatalf("position was not saved: (%v, %v)", saved.X, saved.Y)
	}
}

func TestWorldRequiresLoginBeforeMovement(t *testing.T) {
	store, err := persistence.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	world := NewWorld(store, 100*time.Millisecond)
	client := &testClient{key: "client-1"}
	world.handleEvent(context.Background(), Event{
		Client: client, Type: "position.update", Payload: json.RawMessage(`{"x":1,"y":2}`),
	})
	if got := client.messages[0].Type; got != "error" {
		t.Fatalf("got response %q, want error", got)
	}
}

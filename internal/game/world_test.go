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
			"attributes":{"power":5,"agility":5,"endurance":5,"insight":5}
		}`),
	})
	if got := client.messages[0].Type; got != "character.created" {
		t.Fatalf("got response %q, want character.created", got)
	}
	if got := client.messages[1].Type; got != "world.snapshot" {
		t.Fatalf("got response %q, want world.snapshot", got)
	}

	world.handleEvent(context.Background(), Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":12.5,"y":40}`),
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
	if saved.X != 12.5 || saved.Y != 40 {
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

func TestWorldSnapshotAndMovingMonsterDelta(t *testing.T) {
	world, _ := newTestWorld(t)
	client := &testClient{key: "client-1"}
	createTestCharacter(t, world, client, persistence.Attributes{
		Power: 5, Agility: 5, Endurance: 5, Insight: 5,
	})

	snapshot, ok := client.messages[1].Payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected snapshot payload: %#v", client.messages[1].Payload)
	}
	zone, ok := snapshot["zone"].(zoneInfo)
	if !ok || zone.ID != "mosswake_fen" {
		t.Fatalf("unexpected snapshot zone: %#v", snapshot["zone"])
	}
	npcs, ok := snapshot["npcs"].([]npcState)
	if !ok || len(npcs) != 2 || !npcs[0].QuestGiver || len(npcs[0].AvailableQuests) != 3 {
		t.Fatalf("unexpected snapshot NPCs: %#v", snapshot["npcs"])
	}
	monsters, ok := snapshot["monsters"].([]monsterView)
	if !ok || len(monsters) != 3 {
		t.Fatalf("unexpected snapshot monsters: %#v", snapshot["monsters"])
	}

	before := world.monsters["reedling-01"].X
	world.advanceMonsters()
	world.broadcastDelta()
	if got := client.messages[len(client.messages)-1].Type; got != "world.delta" {
		t.Fatalf("got response %q, want world.delta", got)
	}
	delta := client.messages[len(client.messages)-1].Payload.(map[string]any)
	if world.monsters["reedling-01"].X == before || len(delta["monsters"].([]monsterView)) != 3 {
		t.Fatalf("monster patrol was not reflected in delta: %#v", delta)
	}
}

func TestCombatDamageDeathAndXP(t *testing.T) {
	world, store := newTestWorld(t)
	client := &testClient{key: "fighter"}
	createTestCharacter(t, world, client, persistence.Attributes{
		Power: 10, Agility: 5, Endurance: 2, Insight: 3,
	})

	attack := Event{
		Client: client, Type: "combat.attack",
		Payload: json.RawMessage(`{"target_id":"reedling-01"}`),
	}
	world.handleEvent(context.Background(), attack)
	result := latestMessage(t, client, "combat.result").Payload.(map[string]any)
	if result["damage"] != 10 || result["target_hp_remaining"] != 10 || result["target_defeated"] != false {
		t.Fatalf("unexpected first attack result: %#v", result)
	}

	world.handleEvent(context.Background(), attack)
	result = latestMessage(t, client, "combat.result").Payload.(map[string]any)
	if result["target_hp_remaining"] != 0 || result["target_defeated"] != true {
		t.Fatalf("unexpected killing blow result: %#v", result)
	}
	if _, exists := world.monsters["reedling-01"]; exists {
		t.Fatal("defeated monster remains in the zone")
	}
	xp := latestMessage(t, client, "character.xp").Payload.(map[string]any)
	if xp["gained"] != int64(8) || xp["total"] != int64(8) {
		t.Fatalf("unexpected XP award: %#v", xp)
	}
	saved, err := store.LoadCharacter(context.Background(), "Aster")
	if err != nil {
		t.Fatal(err)
	}
	if saved.XP != 8 {
		t.Fatalf("XP was not persisted: %d", saved.XP)
	}
	world.broadcastDelta()
	delta := client.messages[len(client.messages)-1]
	if delta.Type != "world.delta" {
		t.Fatalf("got response %q, want world.delta after monster death", delta.Type)
	}
	payload := delta.Payload.(map[string]any)
	if removed := payload["removed_monsters"].([]string); len(removed) != 1 || removed[0] != "reedling-01" {
		t.Fatalf("delta did not report removed monster: %#v", payload["removed_monsters"])
	}
}

func TestFireBoltAndCinderMend(t *testing.T) {
	world, store := newTestWorld(t)
	client := &testClient{key: "mage"}
	character := createTestCharacter(t, world, client, persistence.Attributes{
		Power: 4, Agility: 4, Endurance: 6, Insight: 6,
	})
	player := world.players[client.Key()]
	player.character.HP = 30
	if err := store.SaveCharacter(context.Background(), player.character); err != nil {
		t.Fatal(err)
	}

	world.handleEvent(context.Background(), Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":18,"y":19}`),
	})
	world.handleEvent(context.Background(), Event{
		Client: client, Type: "magic.cast",
		Payload: json.RawMessage(`{"spell_id":"fire_ember_bolt","target_id":"lantern-moth-01"}`),
	})
	bolt := latestMessage(t, client, "magic.result").Payload.(map[string]any)
	if bolt["damage"] != 19 || bolt["target_hp_remaining"] != 5 || bolt["caster_mp_remaining"] != 17 {
		t.Fatalf("unexpected Ember Bolt result: %#v", bolt)
	}

	world.handleEvent(context.Background(), Event{
		Client: client, Type: "magic.cast",
		Payload: json.RawMessage(`{"spell_id":"fire_cinder_mend","target_id":"self"}`),
	})
	mend := latestMessage(t, client, "magic.result").Payload.(map[string]any)
	if mend["healing"] != 18 || mend["target_hp_remaining"] != 48 || mend["caster_mp_remaining"] != 13 {
		t.Fatalf("unexpected Cinder Mend result: %#v", mend)
	}
	saved, err := store.LoadCharacter(context.Background(), character.Name)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HP != 48 || saved.MP != 13 {
		t.Fatalf("magic resources were not persisted: hp=%d mp=%d", saved.HP, saved.MP)
	}
}

func TestQuestAcceptKillTalkAndTurnIn(t *testing.T) {
	world, store := newTestWorld(t)
	client := &testClient{key: "quester"}
	character := createTestCharacter(t, world, client, persistence.Attributes{
		Power: 10, Agility: 5, Endurance: 2, Insight: 3,
	})
	ctx := context.Background()
	world.handleEvent(ctx, Event{
		Client: client, Type: "quest.accept",
		Payload: json.RawMessage(`{"quest_id":"fen_patrol","npc_id":"npc_maela"}`),
	})
	if got := latestMessage(t, client, "quest.updated").Payload.(map[string]any)["status"]; got != "accepted" {
		t.Fatalf("quest starts in status %v, want accepted", got)
	}

	attack := Event{
		Client: client, Type: "combat.attack",
		Payload: json.RawMessage(`{"target_id":"reedling-01"}`),
	}
	world.handleEvent(ctx, attack)
	world.handleEvent(ctx, attack)
	world.handleEvent(ctx, Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":18,"y":19}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "combat.attack",
		Payload: json.RawMessage(`{"target_id":"lantern-moth-01"}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "combat.attack",
		Payload: json.RawMessage(`{"target_id":"lantern-moth-01"}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "combat.attack",
		Payload: json.RawMessage(`{"target_id":"lantern-moth-01"}`),
	})
	progress, ok := world.players[client.Key()].quests["fen_patrol"]
	if !ok || progress.Status != "ready" || progress.Progress != 2 {
		t.Fatalf("kill objective did not complete: %+v", progress)
	}

	world.handleEvent(ctx, Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":10,"y":10}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "quest.turn_in",
		Payload: json.RawMessage(`{"quest_id":"fen_patrol","npc_id":"npc_maela"}`),
	})
	progress = world.players[client.Key()].quests["fen_patrol"]
	if progress.Status != "turned_in" {
		t.Fatalf("quest did not turn in: %+v", progress)
	}

	world.handleEvent(ctx, Event{
		Client: client, Type: "quest.accept",
		Payload: json.RawMessage(`{"quest_id":"reedkeeper_message","npc_id":"npc_maela"}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":15,"y":12}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "npc.talk",
		Payload: json.RawMessage(`{"npc_id":"npc_elin"}`),
	})
	progress = world.players[client.Key()].quests["reedkeeper_message"]
	if progress.Status != "ready" || progress.Progress != 1 {
		t.Fatalf("talk objective did not complete: %+v", progress)
	}
	world.handleEvent(ctx, Event{
		Client: client, Type: "position.update",
		Payload: json.RawMessage(`{"x":10,"y":10}`),
	})
	world.handleEvent(ctx, Event{
		Client: client, Type: "quest.turn_in",
		Payload: json.RawMessage(`{"quest_id":"reedkeeper_message","npc_id":"npc_maela"}`),
	})
	if got := world.players[client.Key()].character.XP; got != 45 {
		t.Fatalf("wrong total XP after monster kills and quest rewards: got %d, want 45", got)
	}
	storedQuests, err := store.LoadQuestProgress(ctx, character.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedQuests) != 2 || storedQuests[0].Status != "turned_in" || storedQuests[1].Status != "turned_in" {
		t.Fatalf("quest state transitions were not persisted: %+v", storedQuests)
	}
	savedCharacter, err := store.LoadCharacter(ctx, character.Name)
	if err != nil {
		t.Fatal(err)
	}
	if savedCharacter.XP != 45 {
		t.Fatalf("quest XP reward was not persisted: %d", savedCharacter.XP)
	}
}

func TestLoginRestoresPersistedQuestState(t *testing.T) {
	world, _ := newTestWorld(t)
	firstClient := &testClient{key: "first"}
	createTestCharacter(t, world, firstClient, persistence.Attributes{
		Power: 5, Agility: 5, Endurance: 5, Insight: 5,
	})
	world.handleEvent(context.Background(), Event{
		Client: firstClient, Type: "quest.accept",
		Payload: json.RawMessage(`{"quest_id":"fen_patrol","npc_id":"npc_maela"}`),
	})
	world.handleEvent(context.Background(), Event{Client: firstClient, Type: "disconnect"})

	secondClient := &testClient{key: "second"}
	world.handleEvent(context.Background(), Event{
		Client: secondClient, Type: "auth.login",
		Payload: json.RawMessage(`{"name":"Aster"}`),
	})
	accepted := latestMessage(t, secondClient, "auth.accepted").Payload.(map[string]any)
	quests := accepted["quests"].([]map[string]any)
	if len(quests) != 1 || quests[0]["quest_id"] != "fen_patrol" || quests[0]["status"] != "accepted" {
		t.Fatalf("login did not restore persisted quest state: %#v", accepted["quests"])
	}
}

func newTestWorld(t *testing.T) (*World, *persistence.Store) {
	t.Helper()
	store, err := persistence.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return NewWorld(store, 100*time.Millisecond), store
}

func createTestCharacter(
	t *testing.T,
	world *World,
	client *testClient,
	attributes persistence.Attributes,
) persistence.Character {
	t.Helper()
	payload := map[string]any{
		"name": "Aster",
		"attributes": map[string]int{
			"power": attributes.Power, "agility": attributes.Agility,
			"endurance": attributes.Endurance, "insight": attributes.Insight,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	client.messages = nil
	world.handleEvent(context.Background(), Event{
		Client: client, Type: "character.create", Payload: encoded,
	})
	if got := client.messages[0].Type; got != "character.created" {
		t.Fatalf("got response %q, want character.created", got)
	}
	player := world.players[client.Key()]
	if player == nil {
		t.Fatal("character was not added to the world")
	}
	return player.character
}

func latestMessage(t *testing.T, client *testClient, messageType string) protocol.Message {
	t.Helper()
	for i := len(client.messages) - 1; i >= 0; i-- {
		if client.messages[i].Type == messageType {
			return client.messages[i]
		}
	}
	t.Fatalf("no %q message in %#v", messageType, client.messages)
	return protocol.Message{}
}

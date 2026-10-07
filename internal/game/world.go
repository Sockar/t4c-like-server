package game

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Sockar/t4c-like-server/internal/persistence"
	"github.com/Sockar/t4c-like-server/internal/protocol"
)

const (
	maxCoordinate   = 100000
	maxChatLength   = 500
	attributePoints = 20
)

type Client interface {
	Key() string
	Send(protocol.Message) bool
}

type CharacterStore interface {
	CreateCharacter(context.Context, persistence.Character) (persistence.Character, error)
	LoadCharacter(context.Context, string) (persistence.Character, error)
	SaveCharacter(context.Context, persistence.Character) error
}

type Event struct {
	Client  Client
	Type    string
	Payload json.RawMessage
}

type World struct {
	store        CharacterStore
	tickInterval time.Duration
	events       chan Event
	players      map[string]persistence.Character
	clients      map[string]Client
	dirty        bool
}

func NewWorld(store CharacterStore, tickInterval time.Duration) *World {
	return &World{
		store:        store,
		tickInterval: tickInterval,
		events:       make(chan Event, 128),
		players:      make(map[string]persistence.Character),
		clients:      make(map[string]Client),
	}
}

func (w *World) Submit(ctx context.Context, event Event) error {
	select {
	case w.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *World) Run(ctx context.Context) {
	ticker := time.NewTicker(w.tickInterval)
	defer ticker.Stop()
	defer w.saveOnlineCharacters(context.Background())

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-w.events:
			w.handleEvent(ctx, event)
		case <-ticker.C:
			w.broadcastDelta()
		}
	}
}

func (w *World) handleEvent(ctx context.Context, event Event) {
	if event.Client == nil {
		return
	}
	switch event.Type {
	case "auth.login":
		w.login(ctx, event)
	case "character.create":
		w.createCharacter(ctx, event)
	case "position.update":
		w.updatePosition(ctx, event)
	case "chat.send":
		w.sendChat(event)
	case "disconnect":
		delete(w.players, event.Client.Key())
		delete(w.clients, event.Client.Key())
		w.dirty = true
	default:
		w.sendError(event.Client, "unknown_message", "message type is not supported")
	}
}

type namePayload struct {
	Name string `json:"name"`
}

type createPayload struct {
	Name       string                 `json:"name"`
	Attributes persistence.Attributes `json:"attributes"`
}

type positionPayload struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type chatPayload struct {
	Message string `json:"message"`
}

type playerState struct {
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

func (w *World) login(ctx context.Context, event Event) {
	var payload namePayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || !validName(payload.Name) {
		w.sendError(event.Client, "invalid_name", "name must be 3-16 letters, numbers, or underscores")
		return
	}
	if w.isOnline(event.Client) {
		w.sendError(event.Client, "already_logged_in", "this connection already has a character")
		return
	}
	character, err := w.store.LoadCharacter(ctx, payload.Name)
	if err != nil {
		log.Printf("load character %q: %v", payload.Name, err)
		w.sendError(event.Client, "login_failed", "character was not found")
		return
	}
	if w.isCharacterOnline(character.ID) {
		w.sendError(event.Client, "already_online", "character already has an active connection")
		return
	}
	w.addPlayer(event.Client, character)
	w.send(event.Client, "auth.accepted", map[string]any{
		"name": character.Name, "attributes": character.Attributes, "x": character.X, "y": character.Y,
	})
	w.sendSnapshot(event.Client)
}

func (w *World) createCharacter(ctx context.Context, event Event) {
	var payload createPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil ||
		!validName(payload.Name) ||
		!validAttributes(payload.Attributes) {
		w.sendError(event.Client, "invalid_character", "name or attributes are invalid; distribute 20 points across values from 1 to 10")
		return
	}
	if w.isOnline(event.Client) {
		w.sendError(event.Client, "already_logged_in", "this connection already has a character")
		return
	}
	character, err := w.store.CreateCharacter(ctx, persistence.Character{
		Name:       payload.Name,
		Attributes: payload.Attributes,
	})
	if err != nil {
		log.Printf("create character %q: %v", payload.Name, err)
		w.sendError(event.Client, "creation_failed", "character could not be created")
		return
	}
	w.addPlayer(event.Client, character)
	w.send(event.Client, "character.created", map[string]any{
		"name": character.Name, "attributes": character.Attributes, "x": character.X, "y": character.Y,
	})
	w.dirty = true
}

func (w *World) updatePosition(ctx context.Context, event Event) {
	character, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload positionPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil ||
		math.IsNaN(payload.X) || math.IsNaN(payload.Y) ||
		math.IsInf(payload.X, 0) || math.IsInf(payload.Y, 0) ||
		math.Abs(payload.X) > maxCoordinate || math.Abs(payload.Y) > maxCoordinate {
		w.sendError(event.Client, "invalid_position", "coordinates must be finite and within -100000..100000")
		return
	}

	character.X, character.Y = payload.X, payload.Y
	if err := w.store.SaveCharacter(ctx, character); err != nil {
		log.Printf("save character %q position: %v", character.Name, err)
		w.sendError(event.Client, "save_failed", "position could not be saved")
		return
	}
	w.players[event.Client.Key()] = character
	w.dirty = true
}

func (w *World) sendChat(event Event) {
	character, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload chatPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		w.sendError(event.Client, "invalid_chat", "chat message is invalid")
		return
	}
	payload.Message = strings.TrimSpace(payload.Message)
	if payload.Message == "" || len(payload.Message) > maxChatLength {
		w.sendError(event.Client, "invalid_chat", "chat message must contain 1-500 bytes")
		return
	}
	w.broadcast(protocol.Message{
		Type: "chat.message",
		Payload: map[string]string{
			"from": character.Name, "message": payload.Message,
		},
	})
}

func (w *World) addPlayer(client Client, character persistence.Character) {
	w.clients[client.Key()] = client
	w.players[client.Key()] = character
	w.dirty = true
}

func (w *World) isOnline(client Client) bool {
	_, ok := w.players[client.Key()]
	return ok
}

func (w *World) isCharacterOnline(id int64) bool {
	for _, character := range w.players {
		if character.ID == id {
			return true
		}
	}
	return false
}

func (w *World) sendSnapshot(client Client) {
	w.send(client, "world.snapshot", map[string][]playerState{"players": w.states()})
}

func (w *World) broadcastDelta() {
	if !w.dirty {
		return
	}
	w.broadcast(protocol.Message{
		Type:    "world.delta",
		Payload: map[string][]playerState{"players": w.states()},
	})
	w.dirty = false
}

func (w *World) states() []playerState {
	states := make([]playerState, 0, len(w.players))
	for _, character := range w.players {
		states = append(states, playerState{Name: character.Name, X: character.X, Y: character.Y})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	return states
}

func (w *World) broadcast(message protocol.Message) {
	for _, client := range w.clients {
		w.send(client, message.Type, message.Payload)
	}
}

func (w *World) send(client Client, messageType string, payload any) {
	if !client.Send(protocol.Message{Type: messageType, Payload: payload}) {
		log.Printf("outbound queue full for client %s", client.Key())
	}
}

func (w *World) sendError(client Client, code, message string) {
	w.send(client, "error", map[string]string{"code": code, "message": message})
}

func (w *World) saveOnlineCharacters(ctx context.Context) {
	for key, character := range w.players {
		if err := w.store.SaveCharacter(ctx, character); err != nil {
			log.Printf("save character %q on shutdown: %v", character.Name, err)
		}
		delete(w.players, key)
	}
}

func validName(name string) bool {
	if len(name) < 3 || len(name) > 16 {
		return false
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}

func validAttributes(attributes persistence.Attributes) bool {
	values := []int{attributes.Might, attributes.Reflex, attributes.Insight, attributes.Resolve}
	total := 0
	for _, value := range values {
		if value < 1 || value > 10 {
			return false
		}
		total += value
	}
	return total == attributePoints
}

func (w *World) Validate() error {
	if w.tickInterval <= 0 {
		return fmt.Errorf("tick interval must be positive")
	}
	return nil
}

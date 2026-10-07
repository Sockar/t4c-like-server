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
	attributePoints       = 20
	maxChatLength         = 500
	monsterMoveEveryTicks = 5
	combatRange           = 5
	magicRange            = 8
)

type Client interface {
	Key() string
	Send(protocol.Message) bool
}

type CharacterStore interface {
	CreateCharacter(context.Context, persistence.Character) (persistence.Character, error)
	LoadCharacter(context.Context, string) (persistence.Character, error)
	SaveCharacter(context.Context, persistence.Character) error
	CreateQuestProgress(context.Context, persistence.QuestProgress) error
	LoadQuestProgress(context.Context, int64) ([]persistence.QuestProgress, error)
	SaveCharacterAndQuests(context.Context, persistence.Character, []persistence.QuestProgress) error
}

type Event struct {
	Client  Client
	Type    string
	Payload json.RawMessage
}

type playerState struct {
	character persistence.Character
	quests    map[string]persistence.QuestProgress
}

type World struct {
	store        CharacterStore
	tickInterval time.Duration
	events       chan Event
	players      map[string]*playerState
	clients      map[string]Client
	monsters     map[string]*monsterState
	monsterBase  map[string]*monsterState
	deadMonsters map[string]time.Time
	ticks        uint64
	dirty        bool
	removed      []string
}

func NewWorld(store CharacterStore, tickInterval time.Duration) *World {
	monsters := newMonsters()
	return &World{
		store:        store,
		tickInterval: tickInterval,
		events:       make(chan Event, 128),
		players:      make(map[string]*playerState),
		clients:      make(map[string]Client),
		monsters:     monsters,
		monsterBase:  newMonsters(),
		deadMonsters: make(map[string]time.Time),
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
			w.ticks++
			if w.ticks%monsterMoveEveryTicks == 0 {
				w.advanceMonsters()
			}
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
	case "combat.attack":
		w.attack(ctx, event)
	case "magic.cast":
		w.castMagic(ctx, event)
	case "npc.talk":
		w.talkToNPC(ctx, event)
	case "quest.accept":
		w.acceptQuest(ctx, event)
	case "quest.turn_in":
		w.turnInQuest(ctx, event)
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

type entityTargetPayload struct {
	TargetID string `json:"target_id"`
}

type castPayload struct {
	SpellID  string `json:"spell_id"`
	TargetID string `json:"target_id"`
}

type npcPayload struct {
	NPCID string `json:"npc_id"`
}

type questPayload struct {
	QuestID string `json:"quest_id"`
	NPCID   string `json:"npc_id"`
}

type characterView struct {
	ID         string                 `json:"character_id"`
	Name       string                 `json:"name"`
	Attributes persistence.Attributes `json:"attributes"`
	X          float64                `json:"x"`
	Y          float64                `json:"y"`
	HP         int                    `json:"hp"`
	MaxHP      int                    `json:"max_hp"`
	MP         int                    `json:"mp"`
	MaxMP      int                    `json:"max_mp"`
	XP         int64                  `json:"xp"`
}

type combatOutcome struct {
	TargetHP int
	Defeated bool
	XPGained int64
	Quests   []persistence.QuestProgress
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
		w.sendError(event.Client, "login_failed", "character could not be loaded")
		return
	}
	if w.isCharacterOnline(character.ID) {
		w.sendError(event.Client, "already_online", "character already has an active connection")
		return
	}
	if w.normalizeResources(&character) {
		if err := w.store.SaveCharacter(ctx, character); err != nil {
			log.Printf("normalize character %q resources: %v", character.Name, err)
			w.sendError(event.Client, "login_failed", "character could not be loaded")
			return
		}
	}
	player, err := w.loadPlayer(ctx, character)
	if err != nil {
		log.Printf("load quests for character %q: %v", character.Name, err)
		w.sendError(event.Client, "login_failed", "character quests could not be loaded")
		return
	}
	w.addPlayer(event.Client, player)
	w.send(event.Client, "auth.accepted", characterPayload(character, player.quests))
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
		X:          10,
		Y:          10,
		HP:         maxHP(payload.Attributes),
		MP:         maxMP(payload.Attributes),
	})
	if err != nil {
		log.Printf("create character %q: %v", payload.Name, err)
		w.sendError(event.Client, "creation_failed", "character could not be created")
		return
	}
	player, err := w.loadPlayer(ctx, character)
	if err != nil {
		log.Printf("load quests for new character %q: %v", character.Name, err)
		w.sendError(event.Client, "creation_failed", "character could not be created")
		return
	}
	w.addPlayer(event.Client, player)
	w.send(event.Client, "character.created", characterPayload(character, player.quests))
	w.sendSnapshot(event.Client)
	w.dirty = true
}

func (w *World) loadPlayer(ctx context.Context, character persistence.Character) (*playerState, error) {
	progress, err := w.store.LoadQuestProgress(ctx, character.ID)
	if err != nil {
		return nil, err
	}
	player := &playerState{
		character: character,
		quests:    make(map[string]persistence.QuestProgress, len(progress)),
	}
	for _, item := range progress {
		player.quests[item.QuestID] = item
	}
	return player, nil
}

func (w *World) normalizeResources(character *persistence.Character) bool {
	changed := false
	if character.HP > maxHP(character.Attributes) || character.HP <= 0 {
		character.HP = maxHP(character.Attributes)
		changed = true
	}
	if character.MP > maxMP(character.Attributes) || character.MP < 0 {
		character.MP = maxMP(character.Attributes)
		changed = true
	}
	if math.IsNaN(character.X) || math.IsInf(character.X, 0) || character.X < 0 || character.X > mosswakeFen.Width {
		character.X = 10
		changed = true
	}
	if math.IsNaN(character.Y) || math.IsInf(character.Y, 0) || character.Y < 0 || character.Y > mosswakeFen.Height {
		character.Y = 10
		changed = true
	}
	return changed
}

func (w *World) updatePosition(ctx context.Context, event Event) {
	player, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload positionPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil ||
		math.IsNaN(payload.X) || math.IsNaN(payload.Y) ||
		math.IsInf(payload.X, 0) || math.IsInf(payload.Y, 0) ||
		payload.X < 0 || payload.X > mosswakeFen.Width ||
		payload.Y < 0 || payload.Y > mosswakeFen.Height {
		w.sendError(event.Client, "invalid_position", "coordinates must be finite and within the Mosswake Fen bounds (0..100)")
		return
	}

	character := player.character
	character.X, character.Y = payload.X, payload.Y
	if err := w.store.SaveCharacter(ctx, character); err != nil {
		log.Printf("save character %q position: %v", character.Name, err)
		w.sendError(event.Client, "save_failed", "position could not be saved")
		return
	}
	player.character = character
	w.dirty = true
}

func (w *World) sendChat(event Event) {
	player, ok := w.players[event.Client.Key()]
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
			"from": player.character.Name, "message": payload.Message,
		},
	})
}

func (w *World) attack(ctx context.Context, event Event) {
	player, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload entityTargetPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.TargetID == "" {
		w.sendError(event.Client, "invalid_target", "target_id is required")
		return
	}
	monster := w.monsters[payload.TargetID]
	if monster == nil {
		w.sendError(event.Client, "target_not_found", "monster is not present in this zone")
		return
	}
	if !inRange(player.character.X, player.character.Y, monster.X, monster.Y, combatRange) {
		w.sendError(event.Client, "target_out_of_range", "monster is outside physical attack range")
		return
	}
	damage := max(1, player.character.Attributes.Power+player.character.Attributes.Agility/2-monster.Defense)
	outcome, err := w.damageMonster(ctx, player, monster, damage, 0)
	if err != nil {
		log.Printf("resolve attack by %q against %q: %v", player.character.Name, monster.ID, err)
		w.sendError(event.Client, "combat_failed", "attack could not be resolved")
		return
	}
	w.broadcast(protocol.Message{
		Type: "combat.result",
		Payload: map[string]any{
			"attacker": player.character.Name, "target_id": monster.ID,
			"damage": damage, "target_hp_remaining": outcome.TargetHP,
			"target_defeated": outcome.Defeated,
		},
	})
	w.reportRewards(event.Client, player, outcome, "combat")
}

func (w *World) castMagic(ctx context.Context, event Event) {
	player, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload castPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.SpellID == "" || payload.TargetID == "" {
		w.sendError(event.Client, "invalid_spell", "spell_id and target_id are required")
		return
	}
	spell, ok := fireSpells[payload.SpellID]
	if !ok {
		w.sendError(event.Client, "unknown_spell", "spell is not part of the Fire school")
		return
	}
	if player.character.MP < spell.MPCost {
		w.sendError(event.Client, "insufficient_mp", "not enough MP to cast this spell")
		return
	}

	switch spell.Effect {
	case "damage":
		monster := w.monsters[payload.TargetID]
		if monster == nil {
			w.sendError(event.Client, "target_not_found", "monster is not present in this zone")
			return
		}
		if !inRange(player.character.X, player.character.Y, monster.X, monster.Y, magicRange) {
			w.sendError(event.Client, "target_out_of_range", "monster is outside spell range")
			return
		}
		damage := max(1, player.character.Attributes.Insight*3+player.character.Attributes.Power/2-monster.Defense)
		outcome, err := w.damageMonster(ctx, player, monster, damage, spell.MPCost)
		if err != nil {
			log.Printf("cast %q by %q against %q: %v", spell.ID, player.character.Name, monster.ID, err)
			w.sendError(event.Client, "magic_failed", "spell could not be resolved")
			return
		}
		w.broadcast(protocol.Message{
			Type: "magic.result",
			Payload: map[string]any{
				"caster": player.character.Name, "spell_id": spell.ID, "target_id": monster.ID,
				"damage": damage, "healing": 0, "target_hp_remaining": outcome.TargetHP,
				"caster_mp_remaining": player.character.MP, "target_defeated": outcome.Defeated,
			},
		})
		w.reportRewards(event.Client, player, outcome, spell.ID)
	case "heal_self":
		character := player.character
		character.MP -= spell.MPCost
		healing := spell.PowerBase + player.character.Attributes.Insight*spell.PowerPerInsight
		character.HP = min(maxHP(character.Attributes), character.HP+healing)
		if err := w.store.SaveCharacter(ctx, character); err != nil {
			log.Printf("cast %q by %q: %v", spell.ID, character.Name, err)
			w.sendError(event.Client, "magic_failed", "spell could not be resolved")
			return
		}
		player.character = character
		w.broadcast(protocol.Message{
			Type: "magic.result",
			Payload: map[string]any{
				"caster": character.Name, "spell_id": spell.ID, "target_id": "self",
				"damage": 0, "healing": healing, "target_hp_remaining": character.HP,
				"caster_mp_remaining": character.MP, "target_defeated": false,
			},
		})
		w.dirty = true
	}
}

func (w *World) damageMonster(
	ctx context.Context,
	player *playerState,
	monster *monsterState,
	damage int,
	mpCost int,
) (combatOutcome, error) {
	remaining := max(0, monster.HP-damage)
	character := player.character
	character.MP -= mpCost
	outcome := combatOutcome{TargetHP: remaining, Defeated: remaining == 0}
	var questUpdates []persistence.QuestProgress
	if outcome.Defeated {
		character.XP += monster.XPReward
		outcome.XPGained = monster.XPReward
		questUpdates = w.killQuestUpdates(player, monster.ID)
		if err := w.store.SaveCharacterAndQuests(ctx, character, questUpdates); err != nil {
			return combatOutcome{}, err
		}
		player.character = character
		for _, update := range questUpdates {
			player.quests[update.QuestID] = update
		}
		delete(w.monsters, monster.ID)
		w.deadMonsters[monster.ID] = time.Now().Add(30 * time.Second)
		w.removed = append(w.removed, monster.ID)
	} else {
		if err := w.store.SaveCharacter(ctx, character); err != nil {
			return combatOutcome{}, err
		}
		player.character = character
		monster.HP = remaining
	}
	outcome.Quests = questUpdates
	w.dirty = true
	return outcome, nil
}

func (w *World) reportRewards(client Client, player *playerState, outcome combatOutcome, source string) {
	if outcome.XPGained > 0 {
		w.send(client, "character.xp", map[string]any{
			"character_id": characterID(player.character.ID),
			"total":        player.character.XP,
			"gained":       outcome.XPGained,
			"source":       source,
		})
	}
	for _, progress := range outcome.Quests {
		w.send(client, "quest.updated", questUpdatedPayload(progress))
	}
}

func (w *World) talkToNPC(ctx context.Context, event Event) {
	player, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload npcPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.NPCID == "" {
		w.sendError(event.Client, "invalid_npc", "npc_id is required")
		return
	}
	npc, ok := npcByID(payload.NPCID)
	if !ok {
		w.sendError(event.Client, "npc_not_found", "NPC is not present in this zone")
		return
	}
	if !inRange(player.character.X, player.character.Y, npc.X, npc.Y, combatRange) {
		w.sendError(event.Client, "npc_out_of_range", "NPC is outside interaction range")
		return
	}

	w.send(event.Client, "npc.dialogue", map[string]any{
		"npc_id": npc.ID, "dialogue": npc.Dialogue,
		"available_quests": questSummariesForNPC(npc.ID),
	})
	var updates []persistence.QuestProgress
	for id, progress := range player.quests {
		definition := questCatalog[id]
		if progress.Status != "accepted" || definition.Objective != "talk" || definition.TargetID != npc.ID {
			continue
		}
		progress.Progress++
		if progress.Progress >= definition.Required {
			progress.Status = "ready"
		}
		updates = append(updates, progress)
	}
	if len(updates) == 0 {
		return
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].QuestID < updates[j].QuestID })
	if err := w.store.SaveCharacterAndQuests(ctx, player.character, updates); err != nil {
		log.Printf("save talk quest progress for %q: %v", player.character.Name, err)
		w.sendError(event.Client, "quest_update_failed", "quest progress could not be saved")
		return
	}
	for _, progress := range updates {
		player.quests[progress.QuestID] = progress
		w.send(event.Client, "quest.updated", questUpdatedPayload(progress))
	}
}

func (w *World) acceptQuest(ctx context.Context, event Event) {
	player, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload questPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.QuestID == "" || payload.NPCID == "" {
		w.sendError(event.Client, "invalid_quest", "quest_id and npc_id are required")
		return
	}
	definition, ok := questCatalog[payload.QuestID]
	if !ok {
		w.sendError(event.Client, "quest_not_found", "quest is not available in this zone")
		return
	}
	if !w.isQuestGiverInRange(player, payload.NPCID, definition) {
		w.sendError(event.Client, "quest_giver_required", "quest must be accepted from its quest-giver NPC nearby")
		return
	}
	if existing, ok := player.quests[definition.ID]; ok {
		if existing.Status == "turned_in" {
			w.sendError(event.Client, "quest_complete", "quest has already been turned in")
		} else {
			w.sendError(event.Client, "quest_active", "quest is already active")
		}
		return
	}
	progress := persistence.QuestProgress{
		CharacterID: player.character.ID,
		QuestID:     definition.ID,
		Status:      "accepted",
	}
	if err := w.store.CreateQuestProgress(ctx, progress); err != nil {
		log.Printf("accept quest %q for %q: %v", definition.ID, player.character.Name, err)
		w.sendError(event.Client, "quest_accept_failed", "quest could not be accepted")
		return
	}
	player.quests[definition.ID] = progress
	w.send(event.Client, "quest.updated", questUpdatedPayload(progress))
}

func (w *World) turnInQuest(ctx context.Context, event Event) {
	player, ok := w.players[event.Client.Key()]
	if !ok {
		w.sendError(event.Client, "not_logged_in", "log in or create a character first")
		return
	}
	var payload questPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.QuestID == "" || payload.NPCID == "" {
		w.sendError(event.Client, "invalid_quest", "quest_id and npc_id are required")
		return
	}
	definition, ok := questCatalog[payload.QuestID]
	if !ok {
		w.sendError(event.Client, "quest_not_found", "quest is not available in this zone")
		return
	}
	if !w.isQuestGiverInRange(player, payload.NPCID, definition) {
		w.sendError(event.Client, "quest_giver_required", "quest must be turned in to its quest-giver NPC nearby")
		return
	}
	progress, ok := player.quests[definition.ID]
	if !ok || progress.Status != "ready" {
		w.sendError(event.Client, "quest_not_ready", "quest objectives are not complete")
		return
	}
	progress.Status = "turned_in"
	character := player.character
	character.XP += definition.RewardXP
	if err := w.store.SaveCharacterAndQuests(ctx, character, []persistence.QuestProgress{progress}); err != nil {
		log.Printf("turn in quest %q for %q: %v", definition.ID, character.Name, err)
		w.sendError(event.Client, "quest_turn_in_failed", "quest reward could not be saved")
		return
	}
	player.character = character
	player.quests[definition.ID] = progress
	w.send(event.Client, "quest.updated", questUpdatedPayload(progress))
	w.sendXP(event.Client, character, definition.RewardXP, definition.ID)
}

func (w *World) isQuestGiverInRange(player *playerState, npcID string, definition questDefinition) bool {
	if npcID != definition.GiverID {
		return false
	}
	npc, ok := npcByID(npcID)
	return ok && npc.QuestGiver &&
		inRange(player.character.X, player.character.Y, npc.X, npc.Y, combatRange)
}

func (w *World) killQuestUpdates(player *playerState, monsterID string) []persistence.QuestProgress {
	var updates []persistence.QuestProgress
	for id, progress := range player.quests {
		definition := questCatalog[id]
		if progress.Status != "accepted" ||
			(definition.Objective != "kill_any" &&
				(definition.Objective != "kill_target" || definition.TargetID != monsterID)) {
			continue
		}
		progress.Progress++
		if progress.Progress >= definition.Required {
			progress.Status = "ready"
		}
		updates = append(updates, progress)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].QuestID < updates[j].QuestID })
	return updates
}

func (w *World) sendXP(client Client, character persistence.Character, gained int64, source string) {
	w.send(client, "character.xp", map[string]any{
		"character_id": characterID(character.ID),
		"total":        character.XP,
		"gained":       gained,
		"source":       source,
	})
}

func (w *World) addPlayer(client Client, player *playerState) {
	w.clients[client.Key()] = client
	w.players[client.Key()] = player
	w.dirty = true
}

func (w *World) isOnline(client Client) bool {
	_, ok := w.players[client.Key()]
	return ok
}

func (w *World) isCharacterOnline(id int64) bool {
	for _, player := range w.players {
		if player.character.ID == id {
			return true
		}
	}
	return false
}

func (w *World) sendSnapshot(client Client) {
	w.send(client, "world.snapshot", map[string]any{
		"zone":     mosswakeFen,
		"players":  w.playerViews(),
		"npcs":     w.npcViews(),
		"monsters": w.monsterViews(),
	})
}

func (w *World) broadcastDelta() {
	if !w.dirty && len(w.removed) == 0 {
		return
	}
	w.broadcast(protocol.Message{
		Type: "world.delta",
		Payload: map[string]any{
			"players":          w.playerViews(),
			"monsters":         w.monsterViews(),
			"removed_monsters": append([]string{}, w.removed...),
		},
	})
	w.dirty = false
	w.removed = nil
}

func (w *World) advanceMonsters() {
	now := time.Now()
	for id, respawnAt := range w.deadMonsters {
		if now.Before(respawnAt) {
			continue
		}
		monster := *w.monsterBase[id]
		w.monsters[id] = &monster
		delete(w.deadMonsters, id)
		w.removePendingRemoval(id)
		w.dirty = true
	}
	for _, monster := range w.monsters {
		monster.X += monster.direction * 0.25
		if monster.X >= monster.patrolMaxX {
			monster.X = monster.patrolMaxX
			monster.direction = -1
		} else if monster.X <= monster.patrolMinX {
			monster.X = monster.patrolMinX
			monster.direction = 1
		}
	}
	w.dirty = true
}

func (w *World) removePendingRemoval(id string) {
	for index, removedID := range w.removed {
		if removedID == id {
			w.removed = append(w.removed[:index], w.removed[index+1:]...)
			return
		}
	}
}

func (w *World) playerViews() []characterView {
	players := make([]characterView, 0, len(w.players))
	for _, player := range w.players {
		character := player.character
		players = append(players, characterView{
			ID:         characterID(character.ID),
			Name:       character.Name,
			Attributes: character.Attributes,
			X:          character.X,
			Y:          character.Y,
			HP:         character.HP,
			MaxHP:      maxHP(character.Attributes),
			MP:         character.MP,
			MaxMP:      maxMP(character.Attributes),
			XP:         character.XP,
		})
	}
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })
	return players
}

func (w *World) monsterViews() []monsterView {
	monsters := make([]monsterView, 0, len(w.monsters))
	for _, monster := range w.monsters {
		monsters = append(monsters, monster.monsterView)
	}
	sort.Slice(monsters, func(i, j int) bool { return monsters[i].ID < monsters[j].ID })
	return monsters
}

func (w *World) npcViews() []npcState {
	npcs := make([]npcState, 0, len(mosswakeNPCs))
	for _, npc := range mosswakeNPCs {
		npc.AvailableQuests = questSummariesForNPC(npc.ID)
		npcs = append(npcs, npc)
	}
	return npcs
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
	for key, player := range w.players {
		if err := w.store.SaveCharacter(ctx, player.character); err != nil {
			log.Printf("save character %q on shutdown: %v", player.character.Name, err)
		}
		delete(w.players, key)
	}
}

func (w *World) Validate() error {
	if w.tickInterval <= 0 {
		return fmt.Errorf("tick interval must be positive")
	}
	if w.store == nil {
		return fmt.Errorf("character store is required")
	}
	return nil
}

func characterPayload(
	character persistence.Character,
	questProgress map[string]persistence.QuestProgress,
) map[string]any {
	quests := make([]map[string]any, 0, len(questProgress))
	questIDs := make([]string, 0, len(questProgress))
	for id := range questProgress {
		questIDs = append(questIDs, id)
	}
	sort.Strings(questIDs)
	for _, id := range questIDs {
		quests = append(quests, questUpdatedPayload(questProgress[id]))
	}
	return map[string]any{
		"character_id": characterID(character.ID),
		"name":         character.Name,
		"attributes":   character.Attributes,
		"x":            character.X,
		"y":            character.Y,
		"hp":           character.HP,
		"max_hp":       maxHP(character.Attributes),
		"mp":           character.MP,
		"max_mp":       maxMP(character.Attributes),
		"xp":           character.XP,
		"quests":       quests,
	}
}

func characterID(id int64) string {
	return fmt.Sprintf("character-%d", id)
}

func maxHP(attributes persistence.Attributes) int {
	return 20 + attributes.Endurance*5
}

func maxMP(attributes persistence.Attributes) int {
	return 10 + attributes.Insight*2
}

func inRange(x1, y1, x2, y2, distance float64) bool {
	return math.Hypot(x1-x2, y1-y2) <= distance
}

func npcByID(id string) (npcState, bool) {
	for _, npc := range mosswakeNPCs {
		if npc.ID == id {
			return npc, true
		}
	}
	return npcState{}, false
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
	values := []int{attributes.Power, attributes.Agility, attributes.Endurance, attributes.Insight}
	total := 0
	for _, value := range values {
		if value < 1 || value > 10 {
			return false
		}
		total += value
	}
	return total == attributePoints
}

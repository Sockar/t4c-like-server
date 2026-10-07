# t4c-like-server

Go backend server for a T4C-inspired MMORPG (original design, fan project, not affiliated with Dialsoft/Vircom).

## Run locally

Install Go 1.23 or newer, then start the server from the repository root:

```sh
go run ./cmd/server
```

The server listens on `:8080` and accepts WebSocket connections at `ws://localhost:8080/ws`. It creates `./data/world.db` automatically. Override the listener or database path with `-addr` and `-db`, respectively:

```sh
go run ./cmd/server -addr :9000 -db ./data/dev.db
```

Run the test suite with `go test ./...`.

## WebSocket message protocol

Messages are UTF-8 JSON objects with a required `type` and `payload`:

```json
{"type":"chat.send","payload":{"message":"Hello, wayfarers!"}}
```

The message names in the tables below are canonical; the server does not accept legacy aliases such as `client_hello` or `chat_message`.

### Client-to-server messages

| Type | Exact payload shape | Behavior |
| --- | --- | --- |
| `auth.login` | `{"name":"Aster"}` | Loads a character by name. This is a development stub, not authentication. A character can only be online on one connection. |
| `character.create` | `{"name":"Aster","attributes":{"power":5,"agility":5,"endurance":5,"insight":5}}` | Creates and logs in a character. Names are 3-16 ASCII letters, digits, or underscores. Each attribute must be 1-10 and the four values must total exactly 20. |
| `position.update` | `{"x":12.5,"y":14}` | Saves the logged-in character's position. Coordinates must be finite and within the zone bounds 0-100. New characters start at `(10,10)`. |
| `chat.send` | `{"message":"Hello!"}` | Broadcasts a trimmed, non-empty message of at most 500 UTF-8 bytes to logged-in clients. |
| `combat.attack` | `{"target_id":"reedling-01"}` | Performs a physical attack against a living monster in range (5 zone units). |
| `magic.cast` | `{"spell_id":"fire_ember_bolt","target_id":"lantern-moth-01"}` | Casts a Fire spell. `fire_ember_bolt` targets a monster; `fire_cinder_mend` is self-only and requires `"target_id":"self"`. |
| `npc.talk` | `{"npc_id":"npc_elin"}` | Interacts with a nearby NPC (within 5 zone units). |
| `quest.accept` | `{"quest_id":"fen_patrol","npc_id":"npc_maela"}` | Accepts an available quest from its nearby quest-giver. |
| `quest.turn_in` | `{"quest_id":"fen_patrol","npc_id":"npc_maela"}` | Turns in a ready quest to its nearby quest-giver. |

### Server-to-client messages

All server messages use the same `{"type":"...","payload":{...}}` envelope.

| Type | Exact payload shape | When sent |
| --- | --- | --- |
| `character.created`, `auth.accepted` | `{"character_id":"character-1","name":"Aster","attributes":{"power":5,"agility":5,"endurance":5,"insight":5},"x":10,"y":10,"hp":45,"max_hp":45,"mp":20,"max_mp":20,"xp":0,"quests":[]}` | Character creation or login. The `quests` array contains quest state objects described below. |
| `world.snapshot` | `{"zone":{"id":"mosswake_fen","name":"Mosswake Fen","width":100,"height":100},"players":[{"character_id":"character-1","name":"Aster","attributes":{"power":5,"agility":5,"endurance":5,"insight":5},"x":10,"y":10,"hp":45,"max_hp":45,"mp":20,"max_mp":20,"xp":0}],"npcs":[{"id":"npc_maela","name":"Maela Reedwatch","x":9,"y":9,"dialogue":"The fen keeps what it is given. Take the marked paths, and return with what you learn.","quest_giver":true,"available_quests":[{"quest_id":"fen_patrol","title":"A Quiet Patrol","description":"Defeat two creatures that trouble the Mosswake paths.","objective":"kill_any","target_id":"","required":2,"reward_xp":15},{"quest_id":"moth_observation","title":"Lantern Wing","description":"Defeat the Lantern Moth and bring its trail to an end.","objective":"kill_target","target_id":"lantern-moth-01","required":1,"reward_xp":20},{"quest_id":"reedkeeper_message","title":"A Word by the Water","description":"Speak with Elin of the Lanterns, then return to Maela.","objective":"talk","target_id":"npc_elin","required":1,"reward_xp":10}]},{"id":"npc_elin","name":"Elin of the Lanterns","x":19,"y":12,"dialogue":"Blue lights drift above the water when the evening fog settles.","quest_giver":false,"available_quests":[]}],"monsters":[{"id":"lantern-moth-01","name":"Lantern Moth","x":21,"y":22,"hp":24,"max_hp":24,"attack":5,"defense":1,"xp_reward":12},{"id":"mire-crawler-01","name":"Mire Crawler","x":37,"y":17,"hp":34,"max_hp":34,"attack":6,"defense":4,"xp_reward":18},{"id":"reedling-01","name":"Reedling","x":12,"y":11,"hp":20,"max_hp":20,"attack":3,"defense":2,"xp_reward":8}]}` | Sent immediately after creation or login. Arrays contain all current entities of that kind; player and monster arrays are ordered by ID. NPCs appear in fixed zone order. |
| `world.delta` | `{"players":[{"character_id":"character-1","name":"Aster","attributes":{"power":5,"agility":5,"endurance":5,"insight":5},"x":10,"y":10,"hp":45,"max_hp":45,"mp":20,"max_mp":20,"xp":0}],"monsters":[{"id":"lantern-moth-01","name":"Lantern Moth","x":21,"y":22,"hp":24,"max_hp":24,"attack":5,"defense":1,"xp_reward":12},{"id":"mire-crawler-01","name":"Mire Crawler","x":37,"y":17,"hp":34,"max_hp":34,"attack":6,"defense":4,"xp_reward":18}],"removed_monsters":["reedling-01"]}` | Sent on the 10 Hz game tick when state changes. `players` and `monsters` are the complete current lists; `removed_monsters` contains IDs removed since the previous delta and may be empty. Monsters patrol every five ticks. |
| `chat.message` | `{"from":"Aster","message":"Hello!"}` | Broadcast after an accepted `chat.send`. |
| `combat.result` | `{"attacker":"Aster","target_id":"reedling-01","damage":5,"target_hp_remaining":15,"target_defeated":false}` | Broadcast after a resolved physical attack. On defeat, the target is removed, XP is awarded, and a later `world.delta` lists its ID in `removed_monsters`. |
| `magic.result` | `{"caster":"Aster","spell_id":"fire_ember_bolt","target_id":"lantern-moth-01","damage":16,"healing":0,"target_hp_remaining":8,"caster_mp_remaining":15,"target_defeated":false}` | Broadcast after a resolved spell. `target_hp_remaining` is the monster HP for Ember Bolt or character HP for Cinder Mend. |
| `npc.dialogue` | `{"npc_id":"npc_elin","dialogue":"Blue lights drift above the water when the evening fog settles.","available_quests":[]}` | Sent to the interacting client after `npc.talk`. Quest-givers include their quest summaries in `available_quests`. |
| `quest.updated` | `{"quest_id":"fen_patrol","title":"A Quiet Patrol","status":"accepted","progress":0,"required":2,"objective":"kill_any","target_id":"","reward_xp":15}` | Sent to the character after accepting, progressing, completing, or turning in a quest. Status is `accepted`, `ready`, or `turned_in`. |
| `character.xp` | `{"character_id":"character-1","total":8,"gained":8,"source":"combat"}` | Sent privately when XP is awarded. `source` is `combat`, a spell ID, or a quest ID. |
| `error` | `{"code":"target_not_found","message":"monster is not present in this zone"}` | Sent for invalid messages or rejected actions. |

Character IDs are stable strings `character-<database id>` in the `character_id` field; monster and NPC IDs are the exact IDs in the snapshot. The NPCs and quest-giver dialogue, zone, monsters, spells, and quests are original server content.

### Zone, combat, magic, and quest rules

The single zone is **Mosswake Fen**, a 100-by-100 coordinate space. Its quest-giver is Maela Reedwatch (`npc_maela`); Elin of the Lanterns (`npc_elin`) is the talk-quest objective. The three monster spawns are Reedling (`reedling-01`), Lantern Moth (`lantern-moth-01`), and Mire Crawler (`mire-crawler-01`). Monster patrol movement is server-driven; defeated monsters respawn at full health after 30 seconds.

The character questionnaire uses exactly four attributes: `power`, `agility`, `endurance`, and `insight`. Each is 1-10 and their sum is 20. Maximum HP is `20 + endurance * 5`; maximum MP is `10 + insight * 2`. Characters start at full HP and MP. There is no passive MP regeneration.

Physical damage is `max(1, power + floor(agility / 2) - monster.defense)`. Fire spells are `fire_ember_bolt` (5 MP; damage `max(1, insight * 3 + floor(power / 2) - monster.defense)`, range 8) and `fire_cinder_mend` (4 MP; restores `6 + insight * 2` HP to self, up to max HP). Combat and spell results are broadcast to online characters in the zone.

Maela offers three quests: `fen_patrol` (defeat any two monsters), `moth_observation` (defeat `lantern-moth-01`), and `reedkeeper_message` (talk to `npc_elin`). Quest progress, status, character XP, HP, MP, attributes, and position are stored in SQLite. Quest rewards are granted only when a `ready` quest is turned in to Maela.

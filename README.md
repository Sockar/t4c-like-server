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

Each connection may send:

| Type | Payload | Behavior |
| --- | --- | --- |
| `character.create` | `{"name":"Aster","attributes":{"power":5,"agility":5,"endurance":5,"insight":5}}` | Creates and logs in a character. Names are 3-16 ASCII letters, digits, or underscores. Each attribute is 1-10 and the four values must total 20. |
| `auth.login` | `{"name":"Aster"}` | Loads an existing character. This is a name-only development stub, not real authentication. |
| `position.update` | `{"x":12.5,"y":-4}` | Saves the logged-in character's position; each coordinate must be finite and between -100000 and 100000. |
| `chat.send` | `{"message":"Hello!"}` | Broadcasts a non-empty message of at most 500 UTF-8 bytes to logged-in clients. |

The server responds with `character.created` or `auth.accepted`, and sends `world.snapshot` when a character comes online. Changed player positions are sent as `world.delta` messages on the 10 Hz game tick. Chat is broadcast as `chat.message` with `from` and `message` fields. Errors use `{"type":"error","payload":{"code":"...","message":"..."}}`. Movement and character state are persisted in SQLite; the world, account system, authorization, and gameplay mechanics are intentionally not implemented yet.

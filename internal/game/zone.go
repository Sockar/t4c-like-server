package game

type zoneInfo struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type npcState struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	X               float64        `json:"x"`
	Y               float64        `json:"y"`
	Dialogue        string         `json:"dialogue"`
	QuestGiver      bool           `json:"quest_giver"`
	AvailableQuests []questSummary `json:"available_quests"`
}

type monsterView struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	HP       int     `json:"hp"`
	MaxHP    int     `json:"max_hp"`
	Attack   int     `json:"attack"`
	Defense  int     `json:"defense"`
	XPReward int64   `json:"xp_reward"`
}

type monsterState struct {
	monsterView
	patrolMinX float64
	patrolMaxX float64
	direction  float64
}

var mosswakeFen = zoneInfo{
	ID: "mosswake_fen", Name: "Mosswake Fen", Width: 100, Height: 100,
}

var mosswakeNPCs = []npcState{
	{
		ID: "npc_maela", Name: "Maela Reedwatch", X: 9, Y: 9,
		Dialogue:   "The fen keeps what it is given. Take the marked paths, and return with what you learn.",
		QuestGiver: true,
	},
	{
		ID: "npc_elin", Name: "Elin of the Lanterns", X: 19, Y: 12,
		Dialogue: "Blue lights drift above the water when the evening fog settles.",
	},
}

func newMonsters() map[string]*monsterState {
	spawns := []*monsterState{
		{
			monsterView: monsterView{
				ID: "reedling-01", Name: "Reedling", X: 12, Y: 11,
				HP: 20, MaxHP: 20, Attack: 3, Defense: 2, XPReward: 8,
			},
			patrolMinX: 11, patrolMaxX: 13, direction: 1,
		},
		{
			monsterView: monsterView{
				ID: "lantern-moth-01", Name: "Lantern Moth", X: 21, Y: 22,
				HP: 24, MaxHP: 24, Attack: 5, Defense: 1, XPReward: 12,
			},
			patrolMinX: 20, patrolMaxX: 22, direction: -1,
		},
		{
			monsterView: monsterView{
				ID: "mire-crawler-01", Name: "Mire Crawler", X: 37, Y: 17,
				HP: 34, MaxHP: 34, Attack: 6, Defense: 4, XPReward: 18,
			},
			patrolMinX: 36.5, patrolMaxX: 37.5, direction: 1,
		},
	}
	monsters := make(map[string]*monsterState, len(spawns))
	for _, spawn := range spawns {
		copy := *spawn
		monsters[copy.ID] = &copy
	}
	return monsters
}

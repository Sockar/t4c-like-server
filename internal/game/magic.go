package game

type spellDefinition struct {
	ID              string
	School          string
	MPCost          int
	Effect          string
	PowerBase       int
	PowerPerInsight int
}

var fireSpells = map[string]spellDefinition{
	"fire_ember_bolt": {
		ID: "fire_ember_bolt", School: "Fire", MPCost: 5, Effect: "damage",
	},
	"fire_cinder_mend": {
		ID: "fire_cinder_mend", School: "Fire", MPCost: 4, Effect: "heal_self",
		PowerBase: 6, PowerPerInsight: 2,
	},
}

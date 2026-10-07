package game

import (
	"sort"

	"github.com/Sockar/t4c-like-server/internal/persistence"
)

type questSummary struct {
	QuestID     string `json:"quest_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Objective   string `json:"objective"`
	TargetID    string `json:"target_id"`
	Required    int    `json:"required"`
	RewardXP    int64  `json:"reward_xp"`
}

type questDefinition struct {
	ID          string
	Title       string
	Description string
	GiverID     string
	Objective   string
	TargetID    string
	Required    int
	RewardXP    int64
}

var questCatalog = map[string]questDefinition{
	"fen_patrol": {
		ID: "fen_patrol", Title: "A Quiet Patrol",
		Description: "Defeat two creatures that trouble the Mosswake paths.",
		GiverID:     "npc_maela", Objective: "kill_any", Required: 2, RewardXP: 15,
	},
	"moth_observation": {
		ID: "moth_observation", Title: "Lantern Wing",
		Description: "Defeat the Lantern Moth and bring its trail to an end.",
		GiverID:     "npc_maela", Objective: "kill_target", TargetID: "lantern-moth-01",
		Required: 1, RewardXP: 20,
	},
	"reedkeeper_message": {
		ID: "reedkeeper_message", Title: "A Word by the Water",
		Description: "Speak with Elin of the Lanterns, then return to Maela.",
		GiverID:     "npc_maela", Objective: "talk", TargetID: "npc_elin",
		Required: 1, RewardXP: 10,
	},
}

func questSummariesForNPC(npcID string) []questSummary {
	summaries := make([]questSummary, 0)
	for _, definition := range questCatalog {
		if definition.GiverID != npcID {
			continue
		}
		summaries = append(summaries, questSummary{
			QuestID: definition.ID, Title: definition.Title, Description: definition.Description,
			Objective: definition.Objective, TargetID: definition.TargetID,
			Required: definition.Required, RewardXP: definition.RewardXP,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].QuestID < summaries[j].QuestID })
	return summaries
}

func questUpdatedPayload(progress persistence.QuestProgress) map[string]any {
	definition := questCatalog[progress.QuestID]
	return map[string]any{
		"quest_id":  definition.ID,
		"title":     definition.Title,
		"status":    progress.Status,
		"progress":  progress.Progress,
		"required":  definition.Required,
		"objective": definition.Objective,
		"target_id": definition.TargetID,
		"reward_xp": definition.RewardXP,
	}
}

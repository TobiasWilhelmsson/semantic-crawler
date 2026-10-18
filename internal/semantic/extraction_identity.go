package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

const ExtractionVersion = "semantic-crawler-v3-source-fidelity"

// extractionIdentity describes content-affecting inputs, never credentials or
// operational limits. Hash the complete instruction content even when callers
// supply no precomputed hash. Rules are sorted without mutating repository data.
func extractionIdentity(config CrawlConfig, rules []SkillRule, model SemanticModel) string {
	type ruleIdentity struct {
		ID          int64
		Version     int
		Scope       RuleScope
		Instruction string
	}
	items := make([]ruleIdentity, 0, len(rules))
	for _, r := range rules {
		items = append(items, ruleIdentity{r.ID, r.Version, r.Scope, r.Instruction})
	}
	sort.Slice(items, func(i, j int) bool {
		a, _ := json.Marshal(items[i])
		b, _ := json.Marshal(items[j])
		return string(a) < string(b)
	})
	provider := modelIdentity(model)
	content := struct {
		Version, Prompt, Scope, Model, Mode, Instructions, InstructionHash string
		SkillVersion                                                       int
		Rules                                                              []ruleIdentity
		Provider                                                           string
	}{ExtractionVersion, config.PromptVersion, config.Scope, config.Model, config.ModelMode, config.SourceSkill.Content, config.SourceSkill.Hash, config.SourceSkill.Version, items, provider}
	data, _ := json.Marshal(content)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func modelIdentity(model SemanticModel) string {
	var settings any = fmt.Sprintf("%T", model)
	if client, ok := model.(*OpenAIClient); ok {
		settings = struct {
			Provider, Endpoint, Model string
			MaxTokens                 int
		}{client.config.Provider, client.config.BaseURL, client.config.Model, client.config.MaxTokens}
	}
	encoded, _ := json.Marshal(settings)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

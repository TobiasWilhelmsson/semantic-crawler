package semantic

// RuleSequence survives merging, pruning and checkpoints. Older reports seed it
// from both retained rules and historical references before any are removed.
func (report *ReviewerLearningReport) trackRuleIdentities() {
	for _, rule := range report.Rules {
		report.RuleSequence = max(report.RuleSequence, rule.ID, rule.Version)
		for _, id := range rule.PromotedFromURLRules {
			report.RuleSequence = max(report.RuleSequence, id)
		}
	}
	for _, page := range report.Pages {
		report.RuleSequence = max(report.RuleSequence, page.ReviewerRuleID)
		for _, id := range page.ActiveRuleIDs {
			report.RuleSequence = max(report.RuleSequence, id)
		}
		for _, turn := range page.ReviewerTurnDetails {
			report.RuleSequence = max(report.RuleSequence, turn.RuleID)
		}
	}
}

func (report *ReviewerLearningReport) nextRuleIdentity() int {
	report.trackRuleIdentities()
	report.RuleSequence++
	return report.RuleSequence
}

func (report *ReviewerLearningReport) mergeSourceRules() {
	report.trackRuleIdentities()
	canonical := map[string]int{}
	aliases := map[int]int{}
	for _, rule := range report.Rules {
		if rule.Scope != RuleScopeSource {
			continue
		}
		if id, exists := canonical[rule.Instruction]; exists {
			aliases[rule.ID] = id
		} else {
			canonical[rule.Instruction] = rule.ID
		}
	}
	report.Rules = mergeDuplicateReviewerLearningSourceRules(report.Rules)
	if len(aliases) == 0 {
		return
	}
	resolve := func(id int) int {
		if canonical, exists := aliases[id]; exists {
			return canonical
		}
		return id
	}
	for index := range report.Pages {
		page := &report.Pages[index]
		page.ReviewerRuleID = resolve(page.ReviewerRuleID)
		var active []int
		for _, id := range page.ActiveRuleIDs {
			active = appendUniqueInt(active, resolve(id))
		}
		page.ActiveRuleIDs = active
		for turn := range page.ReviewerTurnDetails {
			page.ReviewerTurnDetails[turn].RuleID = resolve(page.ReviewerTurnDetails[turn].RuleID)
		}
	}
}

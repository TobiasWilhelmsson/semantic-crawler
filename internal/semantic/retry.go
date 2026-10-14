package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type RetryStore interface {
	ReviewRepository
	PageVersionByID(ctx context.Context, id int64) (PageVersion, error)
}

type ReviewRetrier interface {
	Retry(ctx context.Context, candidateID int64) error
}

type RetryProcessor struct {
	store  RetryStore
	model  SemanticModel
	config CrawlConfig
}

func NewRetryProcessor(store RetryStore, model SemanticModel, config CrawlConfig) *RetryProcessor {
	return &RetryProcessor{store: store, model: model, config: config}
}

func (processor *RetryProcessor) Retry(ctx context.Context, candidateID int64) error {
	transactional, ok := processor.store.(ReviewTransactor)
	if !ok {
		return fmt.Errorf("review repository does not support transactions")
	}
	candidate, err := processor.store.Candidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if candidate.Status != CandidateQueued {
		return fmt.Errorf("candidate %d is not queued", candidateID)
	}
	page, err := processor.store.PageVersionByID(ctx, candidate.PageVersionID)
	if err != nil {
		return err
	}
	blocks, _, err := BuildCandidateBlocks(strings.NewReader(page.RawHTML), page.URL)
	if err != nil {
		return err
	}
	rules, err := processor.store.Rules(ctx, candidate.SourceID, candidate.URL)
	if err != nil {
		return err
	}
	appliedRules, _ := json.Marshal(rules)
	extraction, usage, err := processor.model.Extract(ctx, buildExtractionPrompt(processor.config, page, blocks, rules), blocks)
	if err != nil {
		return err
	}
	if err := ValidateExtraction(extraction, blocks); err != nil {
		return err
	}
	sections := sectionsFromDecisions(blocks, extraction.Blocks)
	markdown, err := markdownFromSections(sections)
	if err != nil {
		return err
	}
	blockJSON, _ := json.Marshal(extraction)
	return transactional.WithReviewTransaction(ctx, func(repository ReviewRepository) error {
		current, err := repository.Candidate(ctx, candidateID)
		if err != nil {
			return err
		}
		if current.Status != CandidateQueued {
			return fmt.Errorf("candidate %d is no longer queued (status %s); retry result discarded", candidateID, current.Status)
		}
		current.Status = CandidatePendingReview
		current.Sections = sections
		current.Markdown = markdown
		current.BlockResponse = blockJSON
		current.ExtractionIdentity = extractionIdentity(processor.config, rules, processor.model)
		current.PromptVersion = ExtractionVersion
		current.SourceSkillHash = processor.config.SourceSkill.Hash
		current.SourceSkillVersion = processor.config.SourceSkill.Version
		current.AppliedRules = appliedRules
		current.Model = processor.config.Model
		current.InputTokens = usage.InputTokens
		current.OutputTokens = usage.OutputTokens
		current.LatencyMS = usage.Latency.Milliseconds()
		current.RawResponse = usage.RawResponse
		_, err = repository.SaveCandidate(ctx, current)
		return err
	})
}

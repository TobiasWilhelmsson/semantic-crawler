package models

import (
	"time"

	"gorm.io/datatypes"
)

type SemanticSource struct {
	ID                  int64 `gorm:"primaryKey"`
	Name                string
	SeedURL             string
	Scope               string `gorm:"type:text"`
	AllowedHost         string
	MaxDepth            int
	MaxPages            int
	CurrentSkillVersion int  `gorm:"default:1"`
	Enabled             bool `gorm:"default:true"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type SemanticCrawlRun struct {
	ID                int64   `gorm:"primaryKey"`
	SourceID          int64   `gorm:"index;uniqueIndex:idx_scan_request"`
	RequestKey        *string `gorm:"uniqueIndex:idx_scan_request"`
	RequestIdentity   string
	Mode              string
	Status            string `gorm:"index"`
	Model             string
	PromptVersion     string
	SkillVersion      int
	ConfigurationJSON datatypes.JSON `gorm:"type:jsonb"`
	MetricsJSON       datatypes.JSON `gorm:"type:jsonb"`
	Error             string         `gorm:"type:text"`
	StartedAt         time.Time
	CompletedAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type SemanticQueueItem struct {
	ID           int64  `gorm:"primaryKey"`
	RunID        int64  `gorm:"uniqueIndex:idx_semantic_run_url"`
	CanonicalURL string `gorm:"uniqueIndex:idx_semantic_run_url;type:text"`
	ParentURL    string `gorm:"type:text"`
	Depth        int
	Status       string `gorm:"index"`
	RouteAction  string
	Outcome      string
	AttemptCount int
	LastError    string `gorm:"type:text"`
	DiscoveredAt time.Time
	ProcessedAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SemanticPageVersion struct {
	ExtractionIdentity string
	ID                 int64  `gorm:"primaryKey"`
	SourceID           int64  `gorm:"index:idx_semantic_page_lookup"`
	RunID              int64  `gorm:"index"`
	CanonicalURL       string `gorm:"index:idx_semantic_page_lookup;type:text"`
	Title              string
	StatusCode         int
	RawHTML            string `gorm:"type:text"`
	SnapshotHTML       string `gorm:"type:text"`
	CoreText           string `gorm:"type:text"`
	ContentHash        string `gorm:"type:char(64);index"`
	PreviousHash       string `gorm:"type:char(64)"`
	FetchMode          string
	Processed          bool `gorm:"default:false"`
	LastCheckedAt      time.Time
	ChangedAt          time.Time
	CreatedAt          time.Time
}

type SemanticCandidate struct {
	ExtractionIdentity string
	ID                 int64  `gorm:"primaryKey"`
	SourceID           int64  `gorm:"index"`
	PageVersionID      int64  `gorm:"index"`
	OriginURL          string `gorm:"type:text;index"`
	Status             string `gorm:"index"`
	SupersedesID       int64
	Sections           datatypes.JSON `gorm:"type:jsonb"`
	Markdown           string         `gorm:"type:text"`
	RouteResponse      datatypes.JSON `gorm:"type:jsonb"`
	BlockResponse      datatypes.JSON `gorm:"type:jsonb"`
	PromptVersion      string
	SkillVersion       int
	SourceSkillHash    string
	SourceSkillVersion int
	AppliedRules       datatypes.JSON `gorm:"type:jsonb"`
	Model              string
	InputTokens        int
	OutputTokens       int
	LatencyMS          int64
	RawModelResponse   string `gorm:"type:text"`
	ApprovedBy         string
	ApprovedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type SemanticReview struct {
	ID               int64 `gorm:"primaryKey"`
	CandidateID      int64 `gorm:"index"`
	Reviewer         string
	Action           string
	EditedSections   datatypes.JSON `gorm:"type:jsonb"`
	Instruction      string         `gorm:"type:text"`
	InstructionScope string
	Comment          string `gorm:"type:text"`
	CreatedAt        time.Time
}

type SemanticSkillRule struct {
	ID             int64  `gorm:"primaryKey"`
	SourceID       int64  `gorm:"index"`
	OriginURL      string `gorm:"type:text;index"`
	Scope          string
	Instruction    string `gorm:"type:text"`
	Version        int    `gorm:"index"`
	Status         string `gorm:"index"`
	CreatedBy      string
	OriginReviewID int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SemanticDelivery struct {
	SchemaVersion string
	ID            int64  `gorm:"primaryKey"`
	CandidateID   int64  `gorm:"index"`
	Endpoint      string `gorm:"type:text"`
	PayloadHash   string `gorm:"type:char(64)"`
	Status        string
	StatusCode    int
	ResponseBody  string `gorm:"type:text"`
	AttemptedAt   time.Time
	CreatedAt     time.Time
}

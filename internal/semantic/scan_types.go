package semantic

import (
	"context"
	"errors"
	"time"
)

type ScanState string

const (
	ScanQueued      ScanState = "queued"
	ScanRunning     ScanState = "running"
	ScanCancelling  ScanState = "cancelling"
	ScanCompleted   ScanState = "completed"
	ScanFailed      ScanState = "failed"
	ScanCancelled   ScanState = "cancelled"
	ScanInterrupted ScanState = "interrupted"

	// ScanFinalizationFailed is a UI state; the persisted run is unchanged until recovery.
	ScanFinalizationFailed ScanState = "finalization_failed"
)

func (state ScanState) Active() bool {
	return state == ScanQueued || state == ScanRunning || state == ScanCancelling
}

var ErrScanBusy = errors.New("a scan is already running")
var ErrScanRequestConflict = errors.New("this start request was already used with different settings; open a fresh scan form")
var ErrScanNotOwned = errors.New("this scan is owned by another process; stop it in its terminal")
var ErrScanFinalizationPending = errors.New("the scan has stopped, but its result could not be saved; open its scan details and retry saving the result before starting another scan")
var ErrScanFinalizationNotPending = errors.New("this scan has no result awaiting recovery in this server")
var ErrWorkspaceEmpty = errors.New("this source has not been scanned yet")

type ScanRequest struct {
	ForceReextract                    bool
	SourceSlug, RequestKey, ModelMode string
	MaxPages, MaxDepth                int
}

type FetchSettings struct {
	Delay        time.Duration
	Retries      int
	RetryDelay   time.Duration
	MaxPageBytes int64
}

type scanConfiguration struct {
	CrawlConfig
	SourceSlug string        `json:"source_slug,omitempty"`
	ModelMode  string        `json:"model_mode,omitempty"`
	Fetch      FetchSettings `json:"fetch,omitempty"`
}

type ScanRun struct {
	ID          int64
	SourceID    int64
	SourceSlug  string
	State       ScanState
	Config      CrawlConfig
	ModelMode   string
	Fetch       FetchSettings
	StartedAt   time.Time
	CompletedAt *time.Time
	Summary     CrawlSummary
	Error       string
}

type ScanPage struct {
	URL     string `json:"url"`
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

type ScanProgress struct {
	FinalizationError string     `json:"finalization_error,omitempty"`
	RunID             int64      `json:"run_id"`
	State             ScanState  `json:"state"`
	Elapsed           string     `json:"elapsed"`
	Discovered        int        `json:"discovered"`
	Waiting           int        `json:"waiting"`
	Processing        int        `json:"processing"`
	Finished          int        `json:"finished"`
	Failed            int        `json:"failed"`
	Unchanged         int        `json:"unchanged"`
	Candidates        int        `json:"candidates"`
	Ignored           int        `json:"ignored"`
	NeedsReview       int        `json:"needs_review"`
	Unknown           int        `json:"unknown"`
	CurrentURL        string     `json:"current_url,omitempty"`
	Pages             []ScanPage `json:"pages"`
}

type SourceWorkspace struct {
	Source     SourceEntry
	DBPath     string
	Store      *GormStore
	Reviews    *ReviewService
	Deliveries *DeliveryService
	Retrier    ReviewRetrier
}

type PreparedScan struct {
	Source        SourceEntry
	DBPath        string
	Request       ScanRequest
	Config        CrawlConfig
	Identity      string
	Fetch         FetchSettings
	Fetcher       Fetcher
	Model         SemanticModel
	Workspace     *SourceWorkspace
	OpenWorkspace func(context.Context, bool) (*SourceWorkspace, error)
}

type QueueOutcomeTracker interface {
	RecordQueueOutcome(context.Context, int64, string, string) error
}

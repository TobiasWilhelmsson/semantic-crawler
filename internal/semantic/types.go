package semantic

type BlockRole string

const (
	RoleHeader     BlockRole = "header"
	RoleContent    BlockRole = "content"
	RoleInfoBox    BlockRole = "info_box"
	RoleMetadata   BlockRole = "metadata"
	RoleNavigation BlockRole = "navigation"
	RoleTable      BlockRole = "table"
	RoleMedia      BlockRole = "media"
	RoleMediaGroup BlockRole = "media_group"
	RoleVideo      BlockRole = "video"
)

type BlockType string

const (
	TypeNone     BlockType = "none"
	TypeCritical BlockType = "critical"
	TypeWarning  BlockType = "warning"
	TypeInfo     BlockType = "info"
)

type CandidateBlock struct {
	ID        string `json:"id"`
	Tag       string `json:"tag"`
	Text      string `json:"text"`
	HTML      string `json:"html"`
	Locator   string `json:"locator"`
	LinkCount int    `json:"link_count"`
}

type RouteAction string

const (
	RouteExtract      RouteAction = "extract"
	RouteNavigateOnly RouteAction = "navigate_only"
	RouteIgnore       RouteAction = "ignore"
	RouteNeedsReview  RouteAction = "needs_review"
)

type RouteResponse struct {
	Action     RouteAction `json:"action"`
	Confidence float64     `json:"confidence"`
	Reason     string      `json:"reason"`
}

type BlockDecision struct {
	BlockID   string    `json:"block_id"`
	Include   bool      `json:"include"`
	Role      BlockRole `json:"role"`
	Type      BlockType `json:"type"`
	ParseMode bool      `json:"parse_mode"`
	Reason    string    `json:"reason"`
}

type ExtractionResponse struct {
	PageKind      string          `json:"page_kind"`
	Blocks        []BlockDecision `json:"blocks"`
	Uncertainties []string        `json:"uncertainties"`
}

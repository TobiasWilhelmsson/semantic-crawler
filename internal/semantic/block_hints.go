package semantic

// structuralBlockHints supplies domain-neutral HTML structure hints to live
// models. Demo-specific vocabulary and routing remain in the fixture model.
func structuralBlockHints(block CandidateBlock) (BlockRole, BlockType) {
	switch block.Tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return RoleHeader, TypeNone
	case "aside":
		return RoleInfoBox, TypeInfo
	case "table":
		return RoleTable, TypeNone
	case "figure", "img", "audio":
		return RoleMedia, TypeNone
	case "video":
		return RoleVideo, TypeNone
	case "nav":
		return RoleNavigation, TypeNone
	default:
		return RoleContent, TypeNone
	}
}

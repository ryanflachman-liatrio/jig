package shared

// TreePrefix returns a fixed three-cell branch prefix including its trailing
// space. The caller supplies whether this is the final sibling.
func TreePrefix(last bool) string {
	if last {
		return TreeLastGlyph + " "
	}
	return TreeBranchGlyph + " "
}

// TreeContinuationPrefix returns the fixed three-cell prefix for rows nested
// below a sibling. Final siblings use whitespace; earlier siblings retain the
// vertical continuation rail.
func TreeContinuationPrefix(last bool) string {
	if last {
		return "   "
	}
	return TreeContinueGlyph + " "
}

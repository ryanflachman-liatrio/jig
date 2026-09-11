package monitor

func cloneBlockState(src map[blockKey]bool) map[blockKey]bool {
	dst := make(map[blockKey]bool, len(src))
	for key, expanded := range src {
		dst[key] = expanded
	}
	return dst
}

func cloneTranscriptItemState(src map[transcriptItemKey]bool) map[transcriptItemKey]bool {
	dst := make(map[transcriptItemKey]bool, len(src))
	for key, expanded := range src {
		dst[key] = expanded
	}
	return dst
}

// ensureFamilyExpandedFor expands stepID's foreach family (if any) so a gate
// arriving for one runtime child always finds a row to focus, without forcing
// the operator to have expanded the family manually first (plan: "gate
// entries focus the exact child").
func (m *Model) ensureFamilyExpandedFor(stepID string) {
	i, ok := m.index[stepID]
	if !ok || !m.steps[i].isChild() {
		return
	}
	m.expanded[m.steps[i].parentID] = true
}

func (m Model) stepRowIndex(stepID string) (int, bool) {
	for i, row := range m.visibleRows() {
		if (row.isStepRow() || row.isChildRow()) && row.stepID == stepID {
			return i, true
		}
	}
	return 0, false
}

func (m Model) savedRowIndex(snapshot *gateContextSnapshot) (int, bool) {
	for i, row := range m.visibleRows() {
		if row.kind != snapshot.rowKind || row.stepID != snapshot.stepID {
			continue
		}
		if row.isStepRow() || row.isChildRow() {
			return i, true
		}
		if row.file != nil && row.file.path == snapshot.filePath {
			return i, true
		}
	}
	return 0, false
}

func (m *Model) saveGateContext(targetStep string) {
	rows := m.visibleRows()
	snapshot := &gateContextSnapshot{
		cursor:         m.cursor,
		listOffset:     m.vp.YOffset(),
		chatOffset:     m.chatVP.YOffset(),
		chatAutoScroll: m.chatAutoScroll,
		chatSeenSeq:    m.chatSeenSeq,
		chatItemExpand: cloneTranscriptItemState(m.chatItemExpand),
		chatExpandAll:  m.chatItemExpandAll,
		chatPageEnd:    m.chatPage.End,
		searchQuery:    m.searchQuery,
		filters:        m.filters,
		targetStep:     targetStep,
	}
	if m.cursor >= 0 && m.cursor < len(rows) {
		row := rows[m.cursor]
		snapshot.rowKind = row.kind
		snapshot.stepID = row.stepID
		if row.file != nil {
			snapshot.filePath = row.file.path
		}
	}
	snapshot.chatItem = m.selectedTranscriptItemKey()
	m.gateContext = snapshot
}

func (m *Model) showGateContext(targetStep string) {
	m.ensureFamilyExpandedFor(targetStep)
	targetRow, ok := m.stepRowIndex(targetStep)
	if !ok {
		return
	}
	if m.gateContext == nil {
		m.saveGateContext(targetStep)
	} else {
		m.gateContext.targetStep = targetStep
	}

	m.cursor = targetRow
	m.ensureCursorVisible()
	// Force a reload even if the user was already viewing this step: returning
	// must restore the saved block and scroll state rather than aliasing it.
	m.chatStep = ""
	m.reloadTranscript()
	m.focus = focusTranscript
	m.refreshPanels()
}

func (m *Model) restoreGateContext() {
	snapshot := m.gateContext
	if snapshot == nil {
		return
	}
	m.gateContext = nil

	stepRow, ok := m.stepRowIndex(snapshot.stepID)
	if !ok {
		if len(m.visibleRows()) == 0 {
			return
		}
		stepRow = min(snapshot.cursor, len(m.visibleRows())-1)
	}
	m.cursor = stepRow
	m.chatStep = ""
	m.reloadTranscript()

	if savedRow, found := m.savedRowIndex(snapshot); found {
		m.cursor = savedRow
		m.reloadTranscript()
	}
	if snapshot.chatPageEnd > 0 && snapshot.chatPageEnd != m.chatPage.End {
		m.loadChatBefore(snapshot.chatPageEnd)
	}
	m.filters = snapshot.filters
	m.searchQuery = snapshot.searchQuery
	m.rebuildTranscriptItemState(snapshot.chatItem)
	m.rerunSearch()
	m.chatItemExpand = cloneTranscriptItemState(snapshot.chatItemExpand)
	m.chatItemExpandAll = snapshot.chatExpandAll
	m.chatAutoScroll = snapshot.chatAutoScroll
	m.chatSeenSeq = snapshot.chatSeenSeq
	if m.hasGate() {
		m.focus = focusGate
	} else {
		m.focus = focusSteps
	}
	m.refreshPanels()
	if m.ready {
		m.vp.SetYOffset(snapshot.listOffset)
		if snapshot.chatAutoScroll {
			m.resumeTranscriptFollow()
		} else {
			m.chatVP.SetYOffset(snapshot.chatOffset)
		}
	}
}

func (m *Model) toggleGateContext() {
	if m.focus != focusGate {
		m.restoreGateContext()
		return
	}

	entry, ok := m.activeEntry()
	if !ok {
		return
	}
	targetStep := presentationForGate(entry).contextStep
	if targetStep == "" {
		return
	}
	if m.gateContext != nil && m.gateContext.targetStep == targetStep {
		m.restoreGateContext()
		return
	}
	m.syncActiveTextarea()
	m.showGateContext(targetStep)
}

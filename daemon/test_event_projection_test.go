package main

func testQueryEvents(st *store, workspaceID, sessionID string, afterSeq int64) []AstralEvent {
	return eventProjectionService{store: st}.QueryEvents(workspaceID, sessionID, afterSeq)
}

func testQueryEventsWindow(st *store, workspaceID, sessionID string, afterSeq, beforeSeq int64, limit int) []AstralEvent {
	return eventProjectionService{store: st}.QueryEventsWindow(workspaceID, sessionID, afterSeq, beforeSeq, limit)
}

func testAllEvents(st *store) []AstralEvent {
	return eventProjectionService{store: st}.QueryEvents("", "", 0)
}

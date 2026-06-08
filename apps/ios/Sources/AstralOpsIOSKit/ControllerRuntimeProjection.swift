import Foundation

struct ControllerWorkbenchSelection: Equatable {
    var workspaceID: String = ""
    var sessionID: String = ""
    var terminalID: String = ""
    var sessionsByWorkspace: [String: String] = [:]
}

enum ControllerRuntimeProjection {
    static func selectWorkspace(_ workspaceID: String, in workbench: WorkbenchState, current: ControllerWorkbenchSelection) -> ControllerWorkbenchSelection {
        var next = current
        next.workspaceID = workspaceID
        let remembered = current.sessionsByWorkspace[workspaceID] ?? ""
        if !remembered.isEmpty, workbench.sessions[remembered]?.workspaceID == workspaceID {
            next.sessionID = remembered
        } else {
            next.sessionID = sortedSessions(in: workbench, workspaceID: workspaceID).first?.id ?? ""
        }
        next.terminalID = sortedTerminals(in: workbench, workspaceID: workspaceID).first?.terminalID ?? ""
        return reconcile(next, in: workbench)
    }

    static func selectSession(_ sessionID: String, in workbench: WorkbenchState, current: ControllerWorkbenchSelection) -> ControllerWorkbenchSelection {
        guard let session = workbench.sessions[sessionID] else {
            return reconcile(current, in: workbench)
        }
        var next = current
        next.workspaceID = session.workspaceID
        next.sessionID = sessionID
        next.sessionsByWorkspace[session.workspaceID] = sessionID
        next.terminalID = sortedTerminals(in: workbench, workspaceID: session.workspaceID).first?.terminalID ?? ""
        return reconcile(next, in: workbench)
    }

    static func selectTerminal(_ terminalID: String, in workbench: WorkbenchState, current: ControllerWorkbenchSelection) -> ControllerWorkbenchSelection {
        guard let tab = workbench.terminalTabs[terminalID] else {
            return reconcile(current, in: workbench)
        }
        var next = current
        next.workspaceID = tab.workspaceID
        next.terminalID = tab.terminalID
        return reconcile(next, in: workbench)
    }

    static func reconcile(_ selection: ControllerWorkbenchSelection, in workbench: WorkbenchState) -> ControllerWorkbenchSelection {
        var next = selection
        if next.workspaceID.isEmpty || workbench.workspaces[next.workspaceID] == nil {
            next.workspaceID = sortedWorkspaces(in: workbench).first?.id ?? ""
        }
        if !next.sessionID.isEmpty {
            let session = workbench.sessions[next.sessionID]
            if session == nil || (!next.workspaceID.isEmpty && session?.workspaceID != next.workspaceID) {
                next.sessionID = ""
            }
        }
        if next.sessionID.isEmpty, !next.workspaceID.isEmpty {
            let remembered = next.sessionsByWorkspace[next.workspaceID] ?? ""
            if !remembered.isEmpty, workbench.sessions[remembered]?.workspaceID == next.workspaceID {
                next.sessionID = remembered
            } else {
                next.sessionID = sortedSessions(in: workbench, workspaceID: next.workspaceID).first?.id ?? ""
            }
        }
        if !next.workspaceID.isEmpty, !next.sessionID.isEmpty {
            next.sessionsByWorkspace[next.workspaceID] = next.sessionID
        }
        if next.terminalID.isEmpty || workbench.terminalTabs[next.terminalID]?.workspaceID != next.workspaceID {
            next.terminalID = sortedTerminals(in: workbench, workspaceID: next.workspaceID).first?.terminalID ?? ""
        }
        return next
    }

    static func mergeEvents(
        _ current: [String: [AstralEvent]],
        events: [AstralEvent],
        fallbackSessionID: String?,
        limit: Int = 1000
    ) -> [String: [AstralEvent]] {
        var next = current
        for event in events {
            var stored = event
            if stored.sessionID == nil {
                stored.sessionID = fallbackSessionID
            }
            guard let sessionID = stored.sessionID, !sessionID.isEmpty else { continue }
            var bucket = next[sessionID] ?? []
            if let index = bucket.firstIndex(where: { $0.seq == stored.seq }) {
                bucket[index] = stored
            } else {
                bucket.append(stored)
            }
            bucket.sort { $0.seq < $1.seq }
            if bucket.count > limit {
                bucket = Array(bucket.suffix(limit))
            }
            next[sessionID] = bucket
        }
        return next
    }

    private static func sortedWorkspaces(in workbench: WorkbenchState) -> [Workspace] {
        Array(workbench.workspaces.values).sorted { dateString($0.updatedAt ?? $0.createdAt) > dateString($1.updatedAt ?? $1.createdAt) }
    }

    private static func sortedSessions(in workbench: WorkbenchState, workspaceID: String) -> [SessionRecord] {
        Array(workbench.sessions.values)
            .filter { workspaceID.isEmpty || $0.workspaceID == workspaceID }
            .sorted { dateString($0.updatedAt ?? $0.createdAt) > dateString($1.updatedAt ?? $1.createdAt) }
    }

    private static func sortedTerminals(in workbench: WorkbenchState, workspaceID: String) -> [TerminalTab] {
        Array(workbench.terminalTabs.values)
            .filter { workspaceID.isEmpty || $0.workspaceID == workspaceID }
            .sorted { ($0.outputSeq ?? 0) > ($1.outputSeq ?? 0) }
    }

    private static func dateString(_ value: String?) -> String {
        value ?? ""
    }
}

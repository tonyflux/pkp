package lanshare

import "context"

func terminalTask(state string) bool {
	return state == "ready" || state == "failed" || state == "cancelled"
}

// Dismissal belongs to the visitor session, never to the shared task or its files.
func (m *Manager) dismissTasks(ctx context.Context, sid, taskID, sessionID string) ([]string, error) {
	s, err := m.space(sid, false)
	if err != nil {
		return nil, err
	}
	if err = m.validateSpace(ctx, s); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	visitor, current := m.sessions[sessionID], m.spaces[sid]
	if !m.running || visitor == nil || current == nil || !current.Enabled || ctx.Err() != nil {
		return nil, errForbidden
	}
	if taskID != "" {
		task := m.tasks[taskID]
		if task == nil || task.SpaceID != sid {
			return nil, errForbidden
		}
		if !terminalTask(task.State) {
			return nil, errInvalid
		}
	}
	if visitor.DismissedTasks == nil {
		visitor.DismissedTasks = make(map[string]bool)
	}
	// Task IDs are never reused; prune records evicted by the bounded shared history.
	for id := range visitor.DismissedTasks {
		if m.tasks[id] == nil {
			delete(visitor.DismissedTasks, id)
		}
	}
	removed := []string{}
	for id, task := range m.tasks {
		if task.SpaceID == sid && (taskID == "" || id == taskID) && terminalTask(task.State) {
			visitor.DismissedTasks[id] = true
			removed = append(removed, id)
		}
	}
	return removed, nil
}

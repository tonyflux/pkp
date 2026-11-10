package lanshare

// acquireSleepLocked never publishes an enabled state without a live native request.
func (m *Manager) acquireSleepLocked() error {
	if !sleepPreventionSupported {
		return errSleepPreventionUnsupported
	}
	if m.sleepBlock != nil {
		if err := m.releaseSleepLocked(); err != nil {
			return err
		}
	}
	block, err := m.acquireSleep()
	m.sleepBlock = block
	if err != nil || block == nil {
		m.lastError = "sleep_prevention_failed"
		return errSleepPreventionFailed
	}
	m.sleepBlockActive = true
	if m.lastError == "sleep_prevention_failed" {
		m.lastError = ""
	}
	return nil
}

func (m *Manager) releaseSleepLocked() error {
	if m.sleepBlock == nil {
		if m.lastError == "sleep_prevention_failed" {
			m.lastError = ""
		}
		return nil
	}
	if err := m.sleepBlock.Close(); err != nil {
		m.lastError = "sleep_prevention_failed"
		return errSleepPreventionFailed
	}
	m.sleepBlock, m.sleepBlockActive = nil, false
	if m.lastError == "sleep_prevention_failed" {
		m.lastError = ""
	}
	return nil
}

// SetPreventSleep persists only the preference. Idle-sleep protection exists only
// while the listener runs; screen sleep and explicit system sleep stay available.
func (m *Manager) SetPreventSleep(enabled bool) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errForbidden
	}
	if enabled && !sleepPreventionSupported {
		return errSleepPreventionUnsupported
	}
	acquired := false
	if enabled && m.running && !m.sleepBlockActive {
		if err := m.acquireSleepLocked(); err != nil {
			return err
		}
		acquired = true
	}
	previous := m.preventSleep
	m.preventSleep = enabled
	if err := m.saveLocked(); err != nil {
		m.preventSleep = previous
		if acquired {
			if releaseErr := m.releaseSleepLocked(); releaseErr != nil {
				return releaseErr
			}
		}
		return errStorageUnavailable
	}
	if !enabled {
		return m.releaseSleepLocked()
	}
	return nil
}

package tui

import "immich-video-optimizer/version"

// Option configures the AppModel during initialization.
type Option func(*AppModel)

// WithInitialView sets the starting ViewState of the application.
func WithInitialView(v ViewState) Option {
	return func(m *AppModel) {
		m.state = v
		if v == ViewConnectionSetup {
			m.setupFocus = 0
			m.urlInput.Focus()
			m.tokenInput.Blur()
			m.thresholdInput.Blur()
		}
	}
}

// WithUpdateInfo sets the update information on the AppModel (useful for tests).
func WithUpdateInfo(info *version.UpdateInfo) Option {
	return func(m *AppModel) {
		m.updateInfo = info
	}
}

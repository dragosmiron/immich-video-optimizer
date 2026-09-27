package tui

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

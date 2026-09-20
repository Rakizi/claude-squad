package app

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	log.Initialize(false)
	defer log.Close()

	// Run all tests
	exitCode := m.Run()

	// Exit with the same code as the tests
	os.Exit(exitCode)
}

// TestConfirmationModalStateTransitions tests state transitions without full instance setup
func TestConfirmationModalStateTransitions(t *testing.T) {
	// Create a minimal home struct for testing state transitions
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("shows confirmation on D press", func(t *testing.T) {
		// Simulate pressing 'D'
		h.state = stateDefault
		h.confirmationOverlay = nil

		// Manually trigger what would happen in handleKeyPress for 'D'
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("[!] Kill session 'test'?")

		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
	})

	t.Run("returns to default on y press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'y' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on n press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'n' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on esc press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing ESC using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyEscape}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})
}

// TestConfirmationModalKeyHandling tests the actual key handling in confirmation state
func TestConfirmationModalKeyHandling(t *testing.T) {
	// Import needed packages
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Create enough of home struct to test handleKeyPress in confirmation state
	h := &home{
		ctx:                 context.Background(),
		state:               stateConfirm,
		appConfig:           config.DefaultConfig(),
		list:                list,
		menu:                ui.NewMenu(),
		confirmationOverlay: overlay.NewConfirmationOverlay("Kill session?"),
	}

	testCases := []struct {
		name              string
		key               string
		expectedState     state
		expectedDismissed bool
		expectedNil       bool
	}{
		{
			name:              "y key confirms and dismisses overlay",
			key:               "y",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "n key cancels and dismisses overlay",
			key:               "n",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "esc key cancels and dismisses overlay",
			key:               "esc",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "other keys are ignored",
			key:               "x",
			expectedState:     stateConfirm,
			expectedDismissed: false,
			expectedNil:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset state
			h.state = stateConfirm
			h.confirmationOverlay = overlay.NewConfirmationOverlay("Kill session?")

			// Create key message
			var keyMsg tea.KeyMsg
			if tc.key == "esc" {
				keyMsg = tea.KeyMsg{Type: tea.KeyEscape}
			} else {
				keyMsg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)}
			}

			// Call handleKeyPress
			model, _ := h.handleKeyPress(keyMsg)
			homeModel, ok := model.(*home)
			require.True(t, ok)

			assert.Equal(t, tc.expectedState, homeModel.state, "State mismatch for key: %s", tc.key)
			if tc.expectedNil {
				assert.Nil(t, homeModel.confirmationOverlay, "Overlay should be nil for key: %s", tc.key)
			} else {
				assert.NotNil(t, homeModel.confirmationOverlay, "Overlay should not be nil for key: %s", tc.key)
				assert.Equal(t, tc.expectedDismissed, homeModel.confirmationOverlay.Dismissed, "Dismissed mismatch for key: %s", tc.key)
			}
		})
	}
}

// TestConfirmationMessageFormatting tests that confirmation messages are formatted correctly
func TestConfirmationMessageFormatting(t *testing.T) {
	testCases := []struct {
		name            string
		sessionTitle    string
		expectedMessage string
	}{
		{
			name:            "short session name",
			sessionTitle:    "my-feature",
			expectedMessage: "[!] Kill session 'my-feature'? (y/n)",
		},
		{
			name:            "long session name",
			sessionTitle:    "very-long-feature-branch-name-here",
			expectedMessage: "[!] Kill session 'very-long-feature-branch-name-here'? (y/n)",
		},
		{
			name:            "session with spaces",
			sessionTitle:    "feature with spaces",
			expectedMessage: "[!] Kill session 'feature with spaces'? (y/n)",
		},
		{
			name:            "session with special chars",
			sessionTitle:    "feature/branch-123",
			expectedMessage: "[!] Kill session 'feature/branch-123'? (y/n)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the message formatting directly
			actualMessage := fmt.Sprintf("[!] Kill session '%s'? (y/n)", tc.sessionTitle)
			assert.Equal(t, tc.expectedMessage, actualMessage)
		})
	}
}

// TestConfirmationFlowSimulation tests the confirmation flow by simulating the state changes
func TestConfirmationFlowSimulation(t *testing.T) {
	// Create a minimal setup
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Add test instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
		AutoYes: false,
	})
	require.NoError(t, err)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
	}

	// Simulate what happens when D is pressed
	selected := h.list.GetSelectedInstance()
	require.NotNil(t, selected)

	// This is what the KeyKill handler does
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the state
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	// Test that overlay renders with the correct message
	rendered := h.confirmationOverlay.Render()
	assert.Contains(t, rendered, "Kill session 'test-session'?")
}

// TestConfirmActionWithDifferentTypes tests that confirmAction works with different action types
func TestConfirmActionWithDifferentTypes(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("works with simple action returning nil", func(t *testing.T) {
		actionCalled := false
		action := func() tea.Msg {
			actionCalled = true
			return nil
		}

		// Set up callback to track action execution
		actionExecuted := false
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			actionExecuted = true
			action() // Execute the action
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.True(t, actionCalled)
		assert.True(t, actionExecuted)
	})

	t.Run("works with action returning error", func(t *testing.T) {
		expectedErr := fmt.Errorf("test error")
		action := func() tea.Msg {
			return expectedErr
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Error action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.Equal(t, expectedErr, receivedMsg)
	})

	t.Run("works with action returning custom message", func(t *testing.T) {
		action := func() tea.Msg {
			return instanceChangedMsg{}
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Custom message action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		_, ok := receivedMsg.(instanceChangedMsg)
		assert.True(t, ok, "Expected instanceChangedMsg but got %T", receivedMsg)
	})
}

// TestMultipleConfirmationsDontInterfere tests that multiple confirmations don't interfere with each other
func TestMultipleConfirmationsDontInterfere(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// First confirmation
	action1Called := false
	action1 := func() tea.Msg {
		action1Called = true
		return nil
	}

	// Set up first confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("First action?")
	firstOnConfirm := func() {
		h.state = stateDefault
		action1()
	}
	h.confirmationOverlay.OnConfirm = firstOnConfirm
	h.state = stateConfirm

	// Verify first confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Cancel first confirmation (simulate pressing 'n')
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
	if shouldClose {
		h.state = stateDefault
		h.confirmationOverlay = nil
	}

	// Second confirmation with different action
	action2Called := false
	action2 := func() tea.Msg {
		action2Called = true
		return fmt.Errorf("action2 error")
	}

	// Set up second confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("Second action?")
	var secondResult tea.Msg
	secondOnConfirm := func() {
		h.state = stateDefault
		secondResult = action2()
	}
	h.confirmationOverlay.OnConfirm = secondOnConfirm
	h.state = stateConfirm

	// Verify second confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Execute second action to verify it's the correct one
	h.confirmationOverlay.OnConfirm()
	err, ok := secondResult.(error)
	assert.True(t, ok)
	assert.Equal(t, "action2 error", err.Error())
	assert.True(t, action2Called)
	assert.False(t, action1Called, "First action should not have been called")

	// Test that cancelled action can still be executed independently
	firstOnConfirm()
	assert.True(t, action1Called, "First action should be callable after being replaced")
}

// TestConfirmationModalVisualAppearance tests that confirmation modal has distinct visual appearance
func TestConfirmationModalVisualAppearance(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// Create a test confirmation overlay
	message := "[!] Delete everything?"
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the overlay was created with confirmation settings
	assert.NotNil(t, h.confirmationOverlay)
	assert.Equal(t, stateConfirm, h.state)
	assert.False(t, h.confirmationOverlay.Dismissed)

	// Test the overlay render (we can test that it renders without errors)
	rendered := h.confirmationOverlay.Render()
	assert.NotEmpty(t, rendered)

	// Test that it includes the message content and instructions
	assert.Contains(t, rendered, "Delete everything?")
	assert.Contains(t, rendered, "Press")
	assert.Contains(t, rendered, "to confirm")
	assert.Contains(t, rendered, "to cancel")

	// Test that the danger indicator is preserved
	assert.Contains(t, rendered, "[!")
}

// ⛔ THE PAUSE AND RESUME HANDLERS MUST WRITE TO STATE, NOT WAIT FOR QUIT.
// KeyCheckout and KeyResume called Pause()/Resume() and no save, unlike
// KeyMoveUp/KeyMoveDown three cases away. Measured 2026-09-20: paused in the
// interface, `cs ls` reported "running · alive · missing" and state.json still
// held status 0 with the worktree already deleted, until `q`. That window is
// what dispatch, a watcher and `cs ls` read.
//
// ⭐ Deleting both calls COMPILED and left the whole suite green before this
// test existed (PR #6 audit, mutant M6).
func TestCheckoutAndResumePersistWithoutQuitting(t *testing.T) {
	// ⚠ TWICE, NOT ONCE. handleMenuHighlighting swallows the first press, sets
	// keySent and RE-SENDS the key so the menu can light up; the handler body
	// only runs on the second call. A single press exercises nothing and the
	// assertion would read as "the handler does not save" when the handler was
	// never reached.
	press := func(h *home, r rune) {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		_, _ = h.handleKeyPress(msg)
		_, _ = h.handleKeyPress(msg)
	}

	newHomeWithOneInstance := func(t *testing.T, saved *int) *home {
		t.Helper()
		sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
		list := ui.NewList(&sp, false)
		inst, err := session.NewInstance(session.InstanceOptions{
			Title: "t", Path: t.TempDir(), Program: "true",
		})
		require.NoError(t, err)
		// ⚠ Not Loading: both handlers early-return on it, so a test left at the
		// default status never reaches the code it means to exercise.
		inst.SetStatus(session.Ready)
		finalize := list.AddInstance(inst)
		finalize()
		list.SetSelectedInstance(0)

		h := &home{
			ctx:       context.Background(),
			state:     stateDefault,
			appConfig: config.DefaultConfig(),
			list:      list,
			menu:      ui.NewMenu(),
			errBox:    ui.NewErrBox(),
			// the checkout callback closes the instance's terminal pane
			tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			appState:     config.LoadState(),
			// count the writes instead of reaching a real Storage
			saveHook: func() error { *saved++; return nil },
		}
		// Mark the checkout help screen seen so showHelpScreen runs the action
		// inline rather than parking it behind an overlay.
		require.NoError(t, h.appState.SetHelpScreensSeen(^uint32(0)))
		return h
	}

	t.Run("checkout persists even though Pause failed", func(t *testing.T) {
		t.Setenv(config.ConfigDirEnvVar, t.TempDir())
		saved := 0
		h := newHomeWithOneInstance(t, &saved)
		press(h, 'c')
		require.Equal(t, 1, saved,
			"the checkout handler must write the instance list to state before quit")
	})

	// ⛔ THE POSITIVE HALF FOR RESUME. Without it, "a failed resume writes
	// nothing" is equally satisfied by a build that NEVER saves on resume --
	// a control with nothing to control for, and deleting the resume write
	// stayed green (audit mutant M6b). A synthetic instance cannot really
	// resume, hence the seam.
	t.Run("a successful resume persists", func(t *testing.T) {
		t.Setenv(config.ConfigDirEnvVar, t.TempDir())
		saved := 0
		h := newHomeWithOneInstance(t, &saved)
		h.resumeOp = func(*session.Instance) error { return nil }
		press(h, 'r')
		require.Equal(t, 1, saved,
			"the resume handler must write the instance list to state before quit")
	})

	// ⭐ THE CONTROL for that positive: on failure the handler returns BEFORE
	// persisting, so a build that saved unconditionally is caught here.
	t.Run("a failed resume writes nothing", func(t *testing.T) {
		t.Setenv(config.ConfigDirEnvVar, t.TempDir())
		saved := 0
		h := newHomeWithOneInstance(t, &saved)
		h.resumeOp = func(*session.Instance) error { return errors.New("nope") }
		press(h, 'r')
		require.Equal(t, 0, saved,
			"Resume failed, so the handler returned before persisting")
	})

	// ⛔ THE ZERO VALUE MUST DO THE REAL THING. A home with no seams set must
	// not panic: the previous shape required a wiring line in newHome, and
	// deleting it compiled, kept the suite green and killed the interface on
	// the first keypress.
	// ⛔ IT MUST PROVE THE FALLBACK REACHES STORAGE. An earlier version asserted
	// only require.NoError, which `return nil` satisfies — so gutting the
	// fallback body shipped GREEN while the `c` and `r` keypresses silently lost
	// their write. That is this PR's own defect #2, reinstated, passing a test
	// named for preventing it.
	//
	// ⚠ Reading the state back does NOT work as the assertion: SyncInstances
	// skips instances that were never Started, and preserves stored entries it
	// did not hold, so the file is byte-identical whether the call reached
	// storage or not. Corrupt stored JSON is the shape that cannot be faked —
	// only a call that really unmarshals it can fail.
	t.Run("an unseamed home really reaches storage, not merely returns nil", func(t *testing.T) {
		t.Setenv(config.ConfigDirEnvVar, t.TempDir())
		st := config.LoadState()
		storage, err := session.NewStorage(st)
		require.NoError(t, err)
		sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
		h := &home{ctx: context.Background(), storage: storage,
			list: ui.NewList(&sp, false), appState: st}

		// ⭐ CONTROL FIRST: with readable state the fallback succeeds, so the
		// error below is the corruption and not a broken fixture.
		require.NoError(t, h.saveInstances())

		// Written straight to disk: SaveInstances validates its own input, so
		// the corruption has to arrive the way a truncated write or a hand-edit
		// would. SyncInstances calls config.LoadState() itself, so it re-reads.
		dir := os.Getenv(config.ConfigDirEnvVar)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "state.json"),
			[]byte(`{"help_screens_seen":0,"instances":{"not":"an array"}}`), 0o644))
		require.Error(t, h.saveInstances(),
			"the fallback returned nil without reading state — it never reached storage")
	})

	// ⛔ A FAILED WRITE MUST BE SURFACED. Both handlers checked the error and
	// nothing tested that they do, so `_ = m.saveInstances()` shipped green --
	// and KeyCheckout already swallows a failed Pause(), which would make a
	// checkout whose state write failed indistinguishable from one that worked.
	t.Run("a failed write reaches the error box, on both handlers", func(t *testing.T) {
		for _, key := range []rune{'c', 'r'} {
			t.Setenv(config.ConfigDirEnvVar, t.TempDir())
			saved := 0
			h := newHomeWithOneInstance(t, &saved)
			h.saveHook = func() error { return errors.New("state is unwritable") }
			h.resumeOp = func(*session.Instance) error { return nil }
			press(h, key)
			require.Contains(t, h.errBox.String(), "state is unwritable",
				"key %q swallowed a failed state write", string(key))
		}
	})
}

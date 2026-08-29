package config

import (
	"claude-squad/log"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	StateFileName     = "state.json"
	InstancesFileName = "instances.json"

	// StateFileEnvVar overrides the state FILENAME within the config directory.
	// Use it when several people share one config directory -- a checked-in
	// team config, say -- so each has their own session list
	// (state-alice.json, state-bob.json) while sharing config.json.
	//
	// This is separate from CLAUDE_SQUAD_DIR, which moves the whole directory.
	StateFileEnvVar = "CLAUDE_SQUAD_STATE_FILE"
)

// stateFileName returns the state filename, honouring $CLAUDE_SQUAD_STATE_FILE.
//
// The override is a BARE FILENAME, resolved inside the config directory. A value
// containing a path separator is rejected in favour of the default rather than
// silently writing outside the config dir -- moving the directory is what
// CLAUDE_SQUAD_DIR is for.
func stateFileName() string {
	name := strings.TrimSpace(os.Getenv(StateFileEnvVar))
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return StateFileName
	}
	return name
}

// InstanceStorage handles instance-related operations
type InstanceStorage interface {
	// SaveInstances saves the raw instance data
	SaveInstances(instancesJSON json.RawMessage) error
	// GetInstances returns the raw instance data
	GetInstances() json.RawMessage
	// DeleteAllInstances removes all stored instances
	DeleteAllInstances() error
}

// AppState handles application-level state
type AppState interface {
	// GetHelpScreensSeen returns the bitmask of seen help screens
	GetHelpScreensSeen() uint32
	// SetHelpScreensSeen updates the bitmask of seen help screens
	SetHelpScreensSeen(seen uint32) error
}

// StateManager combines instance storage and app state management
type StateManager interface {
	InstanceStorage
	AppState
}

// State represents the application state that persists between sessions
type State struct {
	// HelpScreensSeen is a bitmask tracking which help screens have been shown
	HelpScreensSeen uint32 `json:"help_screens_seen"`
	// Instances stores the serialized instance data as raw JSON
	InstancesData json.RawMessage `json:"instances"`
}

// DefaultState returns the default state
func DefaultState() *State {
	return &State{
		HelpScreensSeen: 0,
		InstancesData:   json.RawMessage("[]"),
	}
}

// LoadState loads the state from disk. If it cannot be done, we return the default state.
func LoadState() *State {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultState()
	}

	statePath := filepath.Join(configDir, stateFileName())
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default state if file doesn't exist
			defaultState := DefaultState()
			if saveErr := SaveState(defaultState); saveErr != nil {
				log.WarningLog.Printf("failed to save default state: %v", saveErr)
			}
			return defaultState
		}

		log.WarningLog.Printf("failed to get state file: %v", err)
		return DefaultState()
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		log.ErrorLog.Printf("failed to parse state file: %v", err)
		return DefaultState()
	}

	return &state
}

// SaveState saves the state to disk
func SaveState(state *State) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	statePath := filepath.Join(configDir, stateFileName())
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	return os.WriteFile(statePath, data, 0644)
}

// InstanceStorage interface implementation

// SaveInstances saves the raw instance data
func (s *State) SaveInstances(instancesJSON json.RawMessage) error {
	s.InstancesData = instancesJSON
	return SaveState(s)
}

// GetInstances returns the raw instance data
func (s *State) GetInstances() json.RawMessage {
	return s.InstancesData
}

// DeleteAllInstances removes all stored instances
func (s *State) DeleteAllInstances() error {
	s.InstancesData = json.RawMessage("[]")
	return SaveState(s)
}

// AppState interface implementation

// GetHelpScreensSeen returns the bitmask of seen help screens
func (s *State) GetHelpScreensSeen() uint32 {
	return s.HelpScreensSeen
}

// SetHelpScreensSeen updates the bitmask of seen help screens
func (s *State) SetHelpScreensSeen(seen uint32) error {
	s.HelpScreensSeen = seen
	return SaveState(s)
}

// InstancesOnDisk reports what the state FILE holds, keeping apart the three
// outcomes LoadState collapses into one.
//
//	raw, true, nil    the file was read; raw is what is stored (possibly "[]")
//	nil, false, nil   there is no state file yet -- nothing has been stored
//	nil, false, err   COULD NOT LOOK: unreadable or unparseable
//
// ⛔ LoadState MUST NOT be used for a decision that turns on ABSENCE. It returns
// DefaultState() -- an EMPTY instance list -- for a missing file, an unreadable
// file and an unparseable file alike, logs, and moves on. Every one of those
// reads to the caller as "there are no sessions". A caller that treats an empty
// list as "these were deleted" would erase every live session on a transient
// read error, and nothing in the return value would say why.
//
// This is the same distinction the exit codes make: 0 is not 3.
func InstancesOnDisk() (json.RawMessage, bool, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get config directory: %w", err)
	}

	statePath := filepath.Join(configDir, stateFileName())
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to read %s: %w", statePath, err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false, fmt.Errorf("failed to parse %s: %w", statePath, err)
	}
	return state.InstancesData, true, nil
}

package session

import (
	"claude-squad/config"
	"claude-squad/log"
	"encoding/json"
	"fmt"
	"time"
)

// InstanceData represents the serializable data of an Instance
type InstanceData struct {
	Title     string    `json:"title"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	Status    Status    `json:"status"`
	Height    int       `json:"height"`
	Width     int       `json:"width"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	AutoYes   bool      `json:"auto_yes"`

	Program   string          `json:"program"`
	Worktree  GitWorktreeData `json:"worktree"`
	DiffStats DiffStatsData   `json:"diff_stats"`
}

// GitWorktreeData represents the serializable data of a GitWorktree
type GitWorktreeData struct {
	RepoPath         string `json:"repo_path"`
	WorktreePath     string `json:"worktree_path"`
	SessionName      string `json:"session_name"`
	BranchName       string `json:"branch_name"`
	BaseCommitSHA    string `json:"base_commit_sha"`
	IsExistingBranch bool   `json:"is_existing_branch"`
}

// DiffStatsData represents the serializable data of a DiffStats
type DiffStatsData struct {
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Content string `json:"content"`
}

// Storage handles saving and loading instances using the state interface
type Storage struct {
	state config.InstanceStorage

	// seenOnDisk is the set of titles this Storage has OBSERVED in the state
	// file -- what it read at load, plus what it has since written.
	//
	// ⛔ IT IS A THREE-STATE, AND nil IS THE THIRD ONE. A title the caller holds
	// that is absent from disk has two possible causes and they want opposite
	// responses:
	//
	//	the caller CREATED it and has not saved yet   -> must be written
	//	another writer DELETED it (`kill`, a second
	//	  interface) since this process read state    -> must NOT be written
	//
	// Absence alone cannot tell them apart. Having seen the title on disk once
	// can: if it was there and is now gone, somebody removed it.
	//
	//	nil        this Storage has never read the state file -- COULD NOT LOOK,
	//	           so no title may be dropped on the strength of it
	//	title in   observed on disk; absent now means DELETED
	//	title out  never observed; absent now means NEW
	seenOnDisk map[string]struct{}
}

// NewStorage creates a new storage instance
func NewStorage(state config.InstanceStorage) (*Storage, error) {
	return &Storage{
		state: state,
	}, nil
}

// observed records the titles this Storage now knows the state file to hold.
func (s *Storage) observed(titles ...string) {
	seen := make(map[string]struct{}, len(titles))
	for _, t := range titles {
		seen[t] = struct{}{}
	}
	s.seenOnDisk = seen
}

// titlesOf is the title set of a stored slice.
func titlesOf(data []InstanceData) []string {
	out := make([]string, 0, len(data))
	for _, d := range data {
		out = append(out, d.Title)
	}
	return out
}

// SaveInstances saves the list of instances to disk
func (s *Storage) SaveInstances(instances []*Instance) error {
	// Convert instances to InstanceData
	data := make([]InstanceData, 0)
	for _, instance := range instances {
		if instance.Started() {
			data = append(data, instance.ToInstanceData())
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}

	if err := s.state.SaveInstances(jsonData); err != nil {
		return err
	}
	s.observed(titlesOf(data)...)
	return nil
}

// SyncInstances saves the caller's instances without discarding instances that
// another writer added since this process read state.
//
// SaveInstances writes the caller's list verbatim. That is correct for
// DeleteInstance and UpdateInstance, which compute the exact list they intend
// to persist, and wrong for a caller that only means "save what I have":
// anything a second writer added is erased. Because the worktree and tmux
// session are created before the save, the erased session keeps running while
// vanishing from every list, which is indistinguishable from a display bug.
//
// ⚠ It re-reads through config.LoadState(), which reads the state FILE. It must
// NOT use s.state.GetInstances(): that returns State.InstancesData, an in-memory
// field populated once at startup, so the "re-read" would return this process's
// own stale copy and the merge would be a no-op. That mistake passes every test
// written against a fake and fails immediately against a second live writer.
//
// The merge works on InstanceData rather than *Instance because LoadInstances
// calls FromInstanceData, which calls Start(false) and restores a tmux session
// per entry. A save must not attach to anything.
//
// The caller's copy wins for any title it holds; titles only on disk are carried
// through. ⚠ Omitting an instance therefore does NOT delete it -- use
// DeleteInstance, which is why the merge cannot live inside SaveInstances.
//
// ⛔ AND THE MIRROR CASE, WHICH THE ADD-ONLY MERGE GOT WRONG. A title the caller
// HOLDS that is ABSENT from disk has two possible causes, and they want opposite
// responses:
//
//	the caller created it and has not saved yet   -> write it
//	`kill`, or a second interface, DELETED it     -> do NOT write it
//
// The add-only merge answered "write it" for both, so a session removed by
// `claude-squad kill` came back the moment the interface next saved -- pointing
// at a tmux session and a worktree that no longer existed. MEASURED 2026-08-29
// against a throwaway config dir: `kill probe-d` left ["probe-b"], quitting the
// interface left ["probe-b","probe-d"], and probe-d's tmux session and worktree
// were both gone. That is Rakizi/the-lab#29 -- kill DOES prune; the interface
// wrote the entry back.
//
// seenOnDisk separates the two, and its nil case keeps the third outcome:
// a Storage that never read the file cannot tell them apart and must drop
// nothing. So must a Storage whose re-read failed -- see InstancesOnDisk.
func (s *Storage) SyncInstances(instances []*Instance) error {
	data := make([]InstanceData, 0, len(instances))
	held := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		if !instance.Started() {
			continue
		}
		d := instance.ToInstanceData()
		data = append(data, d)
		held[d.Title] = struct{}{}
	}

	// ⚠ InstancesOnDisk, not LoadState: this decision turns on ABSENCE, and
	// LoadState reports a missing, unreadable and unparseable file all as an
	// empty list. Treating that as "everything was deleted" would erase every
	// live session on a transient read error.
	raw, present, readErr := config.InstancesOnDisk()
	canDiscriminate := readErr == nil && present && s.seenOnDisk != nil
	if readErr != nil {
		log.Warningf(
			"COULD NOT LOOK: state file unreadable (%v); keeping every held session, "+
				"including any another writer may have deleted", readErr)
	}

	var stored []InstanceData
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return fmt.Errorf("failed to unmarshal stored instances: %w", err)
		}
	}
	onDisk := make(map[string]struct{}, len(stored))
	for _, d := range stored {
		onDisk[d.Title] = struct{}{}
	}

	if canDiscriminate {
		kept := data[:0]
		for _, d := range data {
			_, stillStored := onDisk[d.Title]
			_, wasStored := s.seenOnDisk[d.Title]
			if !stillStored && wasStored {
				// Observed on disk before, gone now: another writer removed it.
				log.Infof(
					"dropping %q from state: removed by another writer since this process loaded it", d.Title)
				delete(held, d.Title)
				continue
			}
			kept = append(kept, d)
		}
		data = kept
	}

	for _, d := range stored {
		if _, ok := held[d.Title]; !ok {
			data = append(data, d)
		}
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}
	if err := s.state.SaveInstances(jsonData); err != nil {
		return err
	}
	s.observed(titlesOf(data)...)
	return nil
}

// LoadInstances loads the list of instances from disk
func (s *Storage) LoadInstances() ([]*Instance, error) {
	jsonData := s.state.GetInstances()

	var instancesData []InstanceData
	if err := json.Unmarshal(jsonData, &instancesData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}

	// What the store held at this read. SyncInstances needs it to tell a title
	// the caller CREATED from one another writer DELETED -- see seenOnDisk.
	// Recorded before the restore loop so a single unrestorable entry does not
	// also cost us the observation.
	s.observed(titlesOf(instancesData)...)

	instances := make([]*Instance, len(instancesData))
	for i, data := range instancesData {
		instance, err := FromInstanceData(data)
		if err != nil {
			return nil, fmt.Errorf("failed to create instance %s: %w", data.Title, err)
		}
		instances[i] = instance
	}

	return instances, nil
}

// DeleteInstance removes an instance from storage
func (s *Storage) DeleteInstance(title string) error {
	instances, err := s.LoadInstances()
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	found := false
	newInstances := make([]*Instance, 0)
	for _, instance := range instances {
		data := instance.ToInstanceData()
		if data.Title != title {
			newInstances = append(newInstances, instance)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("instance not found: %s", title)
	}

	return s.SaveInstances(newInstances)
}

// UpdateInstance updates an existing instance in storage
func (s *Storage) UpdateInstance(instance *Instance) error {
	instances, err := s.LoadInstances()
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	data := instance.ToInstanceData()
	found := false
	for i, existing := range instances {
		existingData := existing.ToInstanceData()
		if existingData.Title == data.Title {
			instances[i] = instance
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("instance not found: %s", data.Title)
	}

	return s.SaveInstances(instances)
}

// DeleteAllInstances removes all stored instances
func (s *Storage) DeleteAllInstances() error {
	return s.state.DeleteAllInstances()
}

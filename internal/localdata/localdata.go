// Package localdata stores UI configuration and local unread state separately.
package localdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

type SidebarMode string

const (
	All         SidebarMode = "all"
	Visible     SidebarMode = "visible"
	Hidden      SidebarMode = "hidden"
	UnreadFirst SidebarMode = "unread-first"
	UnreadOnly  SidebarMode = "unread-only"
)

func (m SidebarMode) Valid() bool {
	return m == All || m == Visible || m == Hidden || m == UnreadFirst || m == UnreadOnly
}

func (m SidebarMode) Label() string {
	switch m {
	case Visible:
		return "Visible"
	case Hidden:
		return "Hidden"
	case UnreadFirst:
		return "Unread first"
	case UnreadOnly:
		return "Unread only"
	default:
		return "All"
	}
}

func (m SidebarMode) Next() SidebarMode {
	switch m {
	case All:
		return Visible
	case Visible:
		return UnreadFirst
	case UnreadFirst:
		return UnreadOnly
	case UnreadOnly:
		return Hidden
	default:
		return All
	}
}

type MessageOpenTarget string

const (
	Desktop MessageOpenTarget = "desktop"
	Browser MessageOpenTarget = "browser"
)

func (t MessageOpenTarget) Valid() bool { return t == Desktop || t == Browser }

type Config struct {
	SidebarMode         SidebarMode                `json:"sidebar_mode"`
	MessageOpenTarget   MessageOpenTarget          `json:"message_open_target"`
	HiddenConversations map[string]map[string]bool `json:"hidden_conversations,omitempty"`
	LastConversations   map[string]string          `json:"last_conversations,omitempty"`
}

// StartedAt establishes a baseline once per account, including conversations
// that have not been opened yet. Old history starts read; offline arrivals do not.
type ReadState struct {
	StartedAt string            `json:"started_at"`
	LastRead  map[string]string `json:"last_read"`
	Latest    map[string]string `json:"latest,omitempty"`
}

func NewReadState(now time.Time) ReadState {
	return ReadState{StartedAt: fmt.Sprintf("%d.%06d", now.Unix(), now.Nanosecond()/1000), LastRead: map[string]string{}, Latest: map[string]string{}}
}

func (s ReadState) Position(channel string) string {
	return max(s.StartedAt, s.LastRead[channel])
}

type Store struct {
	ConfigPath string
	StatePath  string
}

func DefaultStore() (*Store, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return &Store{ConfigPath: filepath.Join(config, "slakt", "config.json"), StatePath: filepath.Join(state, "slakt", "read-state.json")}, nil
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func (s *Store) LoadConfig() (Config, error) {
	config := Config{SidebarMode: Visible, MessageOpenTarget: Desktop}
	if err := readJSON(s.ConfigPath, &config); err != nil {
		return config, err
	}
	if config.SidebarMode == "" {
		config.SidebarMode = Visible
	}
	if !config.SidebarMode.Valid() {
		return config, fmt.Errorf("invalid sidebar_mode %q in %s", config.SidebarMode, s.ConfigPath)
	}
	if !config.MessageOpenTarget.Valid() {
		return config, fmt.Errorf("invalid message_open_target %q in %s (use desktop or browser)", config.MessageOpenTarget, s.ConfigPath)
	}
	return config, nil
}

func (s *Store) LoadMode() (SidebarMode, error) {
	config, err := s.LoadConfig()
	if err != nil {
		return Visible, err
	}
	return config.SidebarMode, nil
}

// Serialize read/modify/rename across processes. Atomic private files prevent a
// crash from leaving partial JSON; monotonic merges preserve other sessions.
func locked(ctx context.Context, path string, update func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock := flock.New(path + ".lock")
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("local data is locked by another slkt process")
	}
	defer lock.Unlock()
	return update()
}

func writeJSON(path string, value any) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".slkt-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(value)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *Store) SaveMode(ctx context.Context, mode SidebarMode) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid sidebar mode %q", mode)
	}
	return s.saveSetting(ctx, "sidebar_mode", string(mode))
}

func (s *Store) SaveMessageOpenTarget(ctx context.Context, target MessageOpenTarget) error {
	if !target.Valid() {
		return fmt.Errorf("invalid message_open_target %q (use desktop or browser)", target)
	}
	return s.saveSetting(ctx, "message_open_target", string(target))
}

func (s *Store) saveSetting(ctx context.Context, key, value string) error {
	return s.saveConfig(ctx, func(config map[string]json.RawMessage) error {
		config[key], _ = json.Marshal(value)
		return nil
	})
}

func (s *Store) saveConfig(ctx context.Context, update func(map[string]json.RawMessage) error) error {
	return locked(ctx, s.ConfigPath, func() error {
		config := map[string]json.RawMessage{}
		if err := readJSON(s.ConfigPath, &config); err != nil {
			return err
		}
		if config == nil {
			return fmt.Errorf("invalid configuration in %s", s.ConfigPath)
		}
		if err := update(config); err != nil {
			return err
		}
		return writeJSON(s.ConfigPath, config)
	})
}

func (s *Store) LoadHiddenConversations(team, user string) (map[string]bool, error) {
	config, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	hidden := config.HiddenConversations[team+"/"+user]
	if hidden == nil {
		hidden = map[string]bool{}
	}
	return hidden, nil
}

// Save only the account being changed. Read-state saves and sidebar previews
// must not overwrite the last conversation explicitly opened in another session.
func (s *Store) SaveLastConversation(ctx context.Context, team, user, channel string) error {
	if team == "" || user == "" || channel == "" {
		return errors.New("missing account or last conversation")
	}
	return s.saveConfig(ctx, func(config map[string]json.RawMessage) error {
		accounts := map[string]string{}
		if raw, ok := config["last_conversations"]; ok {
			if err := json.Unmarshal(raw, &accounts); err != nil {
				return fmt.Errorf("read last conversations in %s: %w", s.ConfigPath, err)
			}
		}
		if accounts == nil {
			accounts = map[string]string{}
		}
		accounts[team+"/"+user] = channel
		config["last_conversations"], _ = json.Marshal(accounts)
		return nil
	})
}

// Save only changed IDs so stale sessions cannot replace another session's
// choices for unrelated conversations. Explicitly unhiding removes the ID.
func (s *Store) SaveHiddenConversations(ctx context.Context, team, user string, changes map[string]bool) error {
	if team == "" || user == "" {
		return errors.New("missing account for hidden conversations")
	}
	return s.saveConfig(ctx, func(config map[string]json.RawMessage) error {
		accounts := map[string]map[string]bool{}
		if raw, ok := config["hidden_conversations"]; ok {
			if err := json.Unmarshal(raw, &accounts); err != nil {
				return fmt.Errorf("read hidden conversations in %s: %w", s.ConfigPath, err)
			}
		}
		if accounts == nil {
			accounts = map[string]map[string]bool{}
		}
		key := team + "/" + user
		hidden := accounts[key]
		if hidden == nil {
			hidden = map[string]bool{}
		}
		for id, hide := range changes {
			if hide {
				hidden[id] = true
			} else {
				delete(hidden, id)
			}
		}
		if len(hidden) == 0 {
			delete(accounts, key)
		} else {
			accounts[key] = hidden
		}
		config["hidden_conversations"], _ = json.Marshal(accounts)
		return nil
	})
}

type accountsFile struct {
	Accounts map[string]ReadState `json:"accounts"`
}

func readAccounts(path string) (accountsFile, error) {
	var data accountsFile
	if err := readJSON(path, &data); err != nil {
		return data, err
	}
	if data.Accounts == nil {
		data.Accounts = map[string]ReadState{}
	}
	return data, nil
}

func (s *Store) LoadReadState(ctx context.Context, team, user string) (ReadState, error) {
	var state ReadState
	err := locked(ctx, s.StatePath, func() error {
		data, err := readAccounts(s.StatePath)
		if err != nil {
			return err
		}
		key := team + "/" + user
		var exists bool
		state, exists = data.Accounts[key]
		if exists && state.StartedAt == "" {
			return fmt.Errorf("missing started_at for %s in %s", key, s.StatePath)
		}
		if !exists {
			state = NewReadState(time.Now())
			data.Accounts[key] = state
			return writeJSON(s.StatePath, data)
		}
		if state.LastRead == nil {
			state.LastRead = map[string]string{}
		}
		if state.Latest == nil {
			state.Latest = map[string]string{}
		}
		return nil
	})
	return state, err
}

func (s *Store) SaveReadState(ctx context.Context, team, user string, state ReadState) error {
	return locked(ctx, s.StatePath, func() error {
		data, err := readAccounts(s.StatePath)
		if err != nil {
			return err
		}
		key := team + "/" + user
		stored := data.Accounts[key]
		if stored.StartedAt == "" {
			stored.StartedAt = state.StartedAt
		}
		if stored.LastRead == nil {
			stored.LastRead = map[string]string{}
		}
		for channel, ts := range state.LastRead {
			stored.LastRead[channel] = max(stored.LastRead[channel], ts)
		}
		if stored.Latest == nil {
			stored.Latest = map[string]string{}
		}
		for channel, ts := range state.Latest {
			stored.Latest[channel] = max(stored.Latest[channel], ts)
		}
		data.Accounts[key] = stored
		return writeJSON(s.StatePath, data)
	})
}

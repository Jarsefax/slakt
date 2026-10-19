package localdata

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	return &Store{ConfigPath: filepath.Join(root, "config", "config.json"), StatePath: filepath.Join(root, "state", "read-state.json")}
}

func TestDefaultPathsSeparateConfigurationAndState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	store, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.ConfigPath != filepath.Join(root, "config", "slakt", "config.json") || store.StatePath != filepath.Join(root, "state", "slakt", "read-state.json") {
		t.Fatalf("unexpected paths: %+v", store)
	}
	t.Setenv("XDG_STATE_HOME", "")
	store, err = DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if store.StatePath != filepath.Join(home, ".local", "state", "slakt", "read-state.json") {
		t.Fatalf("unexpected default state path: %s", store.StatePath)
	}
}

func TestModeRoundTripPreservesOtherConfiguration(t *testing.T) {
	store := testStore(t)
	mode, err := store.LoadMode()
	if err != nil || mode != All {
		t.Fatalf("default mode = %q, %v", mode, err)
	}
	if err := os.MkdirAll(filepath.Dir(store.ConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ConfigPath, []byte(`{"future_setting":true,"message_open_target":"browser"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, want := range []SidebarMode{UnreadFirst, UnreadOnly, All} {
		if err := store.SaveMode(context.Background(), want); err != nil {
			t.Fatal(err)
		}
		mode, err := store.LoadMode()
		if err != nil || mode != want {
			t.Fatalf("mode = %q, %v, want %q", mode, err, want)
		}
		config := map[string]any{}
		if err := readJSON(store.ConfigPath, &config); err != nil || config["future_setting"] != true {
			t.Fatalf("unrelated setting lost: %v, %v", config, err)
		}
		loaded, err := store.LoadConfig()
		if err != nil || loaded.MessageOpenTarget != Browser {
			t.Fatalf("message open target lost: %+v, %v", loaded, err)
		}
	}
	if _, err := os.Stat(store.StatePath); !os.IsNotExist(err) {
		t.Fatalf("saving mode touched read state: %v", err)
	}
	info, _ := os.Stat(store.ConfigPath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("configuration permissions = %o", info.Mode().Perm())
	}
}

func TestMessageOpenTargetConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, content string
		want          MessageOpenTarget
		invalid       bool
	}{
		{name: "missing file", want: Desktop},
		{name: "missing setting", content: `{"sidebar_mode":"unread-first"}`, want: Desktop},
		{name: "desktop", content: `{"message_open_target":"desktop"}`, want: Desktop},
		{name: "browser", content: `{"message_open_target":"browser"}`, want: Browser},
		{name: "unknown target", content: `{"message_open_target":"both"}`, invalid: true},
		{name: "empty target", content: `{"message_open_target":""}`, invalid: true},
		{name: "wrong type", content: `{"message_open_target":true}`, invalid: true},
		{name: "malformed JSON", content: `{"message_open_target":`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t)
			if test.content != "" {
				if err := os.MkdirAll(filepath.Dir(store.ConfigPath), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store.ConfigPath, []byte(test.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config, err := store.LoadConfig()
			if test.invalid {
				if err == nil {
					t.Fatal("invalid configuration accepted")
				}
			} else if err != nil || config.MessageOpenTarget != test.want {
				t.Fatalf("config = %+v, %v, want target %s", config, err, test.want)
			}
			if test.content != "" {
				data, err := os.ReadFile(store.ConfigPath)
				if err != nil || string(data) != test.content {
					t.Fatalf("loading changed configuration: %s, %v", data, err)
				}
			}
		})
	}
}

func TestReadPositionsSurviveRestartAndSeparateAccounts(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	first, err := store.LoadReadState(ctx, "T1", "U1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Position("CNEW") != first.StartedAt || first.StartedAt == "" {
		t.Fatal("unopened conversations have no initial baseline")
	}
	// Two stale snapshots must merge without losing another conversation or
	// allowing an older session to move a read position backwards.
	second, err := store.LoadReadState(ctx, "T1", "U1")
	if err != nil {
		t.Fatal(err)
	}
	first.LastRead["C1"] = "2000000000.000002"
	second.LastRead["C1"] = "2000000000.000001"
	second.LastRead["C2"] = "2000000000.000003"
	for _, state := range []ReadState{first, second} {
		if err := store.SaveReadState(ctx, "T1", "U1", state); err != nil {
			t.Fatal(err)
		}
	}
	restarted := &Store{ConfigPath: store.ConfigPath, StatePath: store.StatePath}
	got, err := restarted.LoadReadState(ctx, "T1", "U1")
	if err != nil || got.StartedAt != first.StartedAt || got.LastRead["C1"] != first.LastRead["C1"] || got.LastRead["C2"] != second.LastRead["C2"] {
		t.Fatalf("read positions lost: %+v, %v", got, err)
	}
	for _, account := range [][2]string{{"T2", "U1"}, {"T1", "U2"}} {
		other, err := restarted.LoadReadState(ctx, account[0], account[1])
		if err != nil || len(other.LastRead) != 0 {
			t.Fatalf("read positions leaked between accounts: %+v, %v", other, err)
		}
	}
	if _, err := os.Stat(store.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("saving read state touched configuration: %v", err)
	}
	info, _ := os.Stat(store.StatePath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("read-state permissions = %o", info.Mode().Perm())
	}
}

func TestConcurrentConfigurationSavesMerge(t *testing.T) {
	store := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	start := make(chan struct{})
	for i := range 8 {
		wg.Go(func() {
			<-start
			other := &Store{ConfigPath: store.ConfigPath}
			if i%2 == 0 {
				errs <- other.SaveMode(ctx, UnreadOnly)
			} else {
				errs <- other.SaveMessageOpenTarget(ctx, Browser)
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	config, err := store.LoadConfig()
	if err != nil || config.SidebarMode != UnreadOnly || config.MessageOpenTarget != Browser {
		t.Fatalf("concurrent config saves lost settings: %+v, %v", config, err)
	}
}

func TestConcurrentReadPositionSavesMerge(t *testing.T) {
	store := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := store.LoadReadState(ctx, "T1", "U1")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			otherStore := &Store{StatePath: store.StatePath}
			errs <- otherStore.SaveReadState(ctx, "T1", "U1", ReadState{StartedAt: state.StartedAt, LastRead: map[string]string{string(rune('A' + i)): "2000000000.000001"}})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.LoadReadState(ctx, "T1", "U1")
	if err != nil || len(got.LastRead) != 8 {
		t.Fatalf("concurrent saves lost positions: %+v, %v", got, err)
	}
}

func TestMalformedFilesAreReportedAndPreserved(t *testing.T) {
	store := testStore(t)
	if err := os.MkdirAll(filepath.Dir(store.ConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{`{"sidebar_mode":`, `{"sidebar_mode":"invalid"}`} {
		if err := os.WriteFile(store.ConfigPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadMode(); err == nil {
			t.Fatalf("accepted invalid configuration: %s", content)
		}
	}
	invalid := []byte(`{"accounts":`)
	if err := os.MkdirAll(filepath.Dir(store.StatePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StatePath, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadReadState(context.Background(), "T1", "U1"); err == nil {
		t.Fatal("accepted invalid read state")
	}
	if err := store.SaveReadState(context.Background(), "T1", "U1", NewReadState(time.Now())); err == nil {
		t.Fatal("overwrote invalid read state")
	}
	got, _ := os.ReadFile(store.StatePath)
	if string(got) != string(invalid) {
		t.Fatal("invalid read state was silently reset")
	}
}

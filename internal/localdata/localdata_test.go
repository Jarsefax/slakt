package localdata

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	if err != nil || mode != Visible {
		t.Fatalf("default mode = %q, %v", mode, err)
	}
	if err := os.MkdirAll(filepath.Dir(store.ConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ConfigPath, []byte(`{"future_setting":true,"message_open_target":"browser"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, want := range []SidebarMode{Visible, UnreadFirst, UnreadOnly, Hidden, All} {
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
	first.Latest["C1"] = "2000000000.000004"
	second.Latest["C1"] = "2000000000.000003"
	second.Latest["C2"] = "2000000000.000003"
	for _, state := range []ReadState{first, second, {StartedAt: first.StartedAt}} {
		if err := store.SaveReadState(ctx, "T1", "U1", state); err != nil {
			t.Fatal(err)
		}
	}
	restarted := &Store{ConfigPath: store.ConfigPath, StatePath: store.StatePath}
	got, err := restarted.LoadReadState(ctx, "T1", "U1")
	if err != nil || got.StartedAt != first.StartedAt || got.LastRead["C1"] != first.LastRead["C1"] || got.LastRead["C2"] != second.LastRead["C2"] {
		t.Fatalf("read positions lost: %+v, %v", got, err)
	}
	if got.Latest["C1"] != first.Latest["C1"] || got.Latest["C2"] != second.Latest["C2"] {
		t.Fatalf("latest observations lost or moved backwards: %+v", got)
	}
	for _, account := range [][2]string{{"T2", "U1"}, {"T1", "U2"}} {
		other, err := restarted.LoadReadState(ctx, account[0], account[1])
		if err != nil || len(other.LastRead) != 0 || len(other.Latest) != 0 {
			t.Fatalf("unread state leaked between accounts: %+v, %v", other, err)
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

func TestLegacyReadStateLoadsWithoutLatestObservations(t *testing.T) {
	store := testStore(t)
	if err := os.MkdirAll(filepath.Dir(store.StatePath), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"accounts":{"T1/U1":{"started_at":"1000000000.000000","last_read":{"C1":"2000000000.000001"}}}}`
	if err := os.WriteFile(store.StatePath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state, err := store.LoadReadState(ctx, "T1", "U1")
	if err != nil || state.StartedAt != "1000000000.000000" || state.Position("C1") != "2000000000.000001" || state.Latest == nil || len(state.Latest) != 0 {
		t.Fatalf("legacy read positions were lost or observations invented: %+v, %v", state, err)
	}
	state.Latest["C1"] = "2000000000.000002"
	if err := store.SaveReadState(ctx, "T1", "U1", state); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadReadState(ctx, "T1", "U1")
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("migrated unread state did not survive restart: %+v, %v", got, err)
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
			channel := string(rune('A' + i))
			errs <- otherStore.SaveReadState(ctx, "T1", "U1", ReadState{StartedAt: state.StartedAt, LastRead: map[string]string{channel: "2000000000.000001"}, Latest: map[string]string{channel: "2000000000.000002"}})
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
	if err != nil || len(got.LastRead) != 8 || len(got.Latest) != 8 {
		t.Fatalf("concurrent saves lost unread state: %+v, %v", got, err)
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

func TestHiddenConversationsPersistAndMergePerAccount(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if err := store.SaveMode(ctx, Hidden); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMessageOpenTarget(ctx, Browser); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		team, user string
		ids        map[string]bool
	}{
		{"T1", "U1", map[string]bool{"C1": true, "D1": true}},
		{"T1", "U1", map[string]bool{"G1": true}},
		{"T2", "U1", map[string]bool{"C1": true}},
		{"T1", "U2", map[string]bool{"D1": true}},
		{"T1", "U1", map[string]bool{"C1": false}},
	} {
		if err := store.SaveHiddenConversations(ctx, change.team, change.user, change.ids); err != nil {
			t.Fatal(err)
		}
	}
	restarted := &Store{ConfigPath: store.ConfigPath, StatePath: store.StatePath}
	for _, account := range []struct {
		team, user string
		want       map[string]bool
	}{
		{"T1", "U1", map[string]bool{"D1": true, "G1": true}},
		{"T2", "U1", map[string]bool{"C1": true}},
		{"T1", "U2", map[string]bool{"D1": true}},
		{"T3", "U3", map[string]bool{}},
	} {
		got, err := restarted.LoadHiddenConversations(account.team, account.user)
		if err != nil || !reflect.DeepEqual(got, account.want) {
			t.Fatalf("hidden for %s/%s = %v, %v; want %v", account.team, account.user, got, err, account.want)
		}
	}
	// A later mode/target save must preserve the account's hidden choices.
	if err := store.SaveMode(ctx, Visible); err != nil {
		t.Fatal(err)
	}
	config, err := store.LoadConfig()
	if err != nil || config.SidebarMode != Visible || config.MessageOpenTarget != Browser || len(config.HiddenConversations["T1/U1"]) != 2 {
		t.Fatalf("settings lost: %+v, %v", config, err)
	}
	if _, err := os.Stat(store.StatePath); !os.IsNotExist(err) {
		t.Fatalf("hiding touched read positions: %v", err)
	}
	info, _ := os.Stat(store.ConfigPath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("configuration permissions = %o", info.Mode().Perm())
	}
}

func TestConcurrentHiddenConversationSavesPreserveOtherSettings(t *testing.T) {
	store := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.SaveHiddenConversations(ctx, "T1", "U1", map[string]bool{"OLD": true}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := range 8 {
		wg.Go(func() {
			errs <- store.SaveHiddenConversations(ctx, "T1", "U1", map[string]bool{string(rune('A' + i)): true, "OLD": false})
		})
	}
	wg.Go(func() { errs <- store.SaveMode(ctx, UnreadOnly) })
	wg.Go(func() { errs <- store.SaveMessageOpenTarget(ctx, Browser) })
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	config, err := store.LoadConfig()
	if err != nil || config.SidebarMode != UnreadOnly || config.MessageOpenTarget != Browser || len(config.HiddenConversations["T1/U1"]) != 8 || config.HiddenConversations["T1/U1"]["OLD"] {
		t.Fatalf("concurrent saves lost choices: %+v, %v", config, err)
	}
}

func TestInvalidHiddenConversationsAreReportedAndPreserved(t *testing.T) {
	store := testStore(t)
	if err := os.MkdirAll(filepath.Dir(store.ConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	content := `{"hidden_conversations":{"T1/U1":{"C1":"yes"}},"future_setting":true}`
	if err := os.WriteFile(store.ConfigPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadHiddenConversations("T1", "U1"); err == nil {
		t.Fatal("invalid hidden choices loaded")
	}
	if err := store.SaveHiddenConversations(context.Background(), "T1", "U1", map[string]bool{"C2": true}); err == nil {
		t.Fatal("invalid hidden choices overwritten")
	}
	data, err := os.ReadFile(store.ConfigPath)
	if err != nil || string(data) != content {
		t.Fatalf("invalid configuration changed: %s, %v", data, err)
	}
}

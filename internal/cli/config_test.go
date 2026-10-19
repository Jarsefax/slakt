package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configPaths(t *testing.T) (configPath, statePath string) {
	t.Helper()
	root := t.TempDir()
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	return filepath.Join(configDir, "slakt", "config.json"), stateDir
}

func TestConfigGetReportsDefaultWithoutCreatingFiles(t *testing.T) {
	configPath, statePath := configPaths(t)
	for _, args := range [][]string{
		{"config", "get", "message_open_target"},
		{"config", "get", "message_open_target", "--json"},
	} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		if args[len(args)-1] == "--json" {
			var value map[string]string
			if err := json.Unmarshal([]byte(out), &value); err != nil || len(value) != 1 || value["message_open_target"] != "desktop" {
				t.Fatalf("default JSON = %q, %v", out, err)
			}
		} else if out != "desktop\n" {
			t.Fatalf("default value = %q", out)
		}
	}
	for _, path := range []string{filepath.Dir(configPath), statePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("config get created %s: %v", path, err)
		}
	}
}

func TestConfigSetPersistsAndPreservesOtherSettings(t *testing.T) {
	configPath, statePath := configPaths(t)
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"sidebar_mode":"unread-only","future_setting":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"browser", "desktop"} {
		out, err := run(t, "config", "set", "message_open_target", target)
		if err != nil || !strings.Contains(out, "message_open_target = "+target) || !strings.Contains(out, "Restart slkt") {
			t.Fatalf("set output = %q, %v", out, err)
		}
		out, err = run(t, "config", "get", "message_open_target")
		if err != nil || out != target+"\n" {
			t.Fatalf("saved value = %q, %v", out, err)
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var saved struct {
			SidebarMode       string          `json:"sidebar_mode"`
			MessageOpenTarget string          `json:"message_open_target"`
			FutureSetting     map[string]bool `json:"future_setting"`
		}
		if err := json.Unmarshal(data, &saved); err != nil || saved.SidebarMode != "unread-only" || saved.MessageOpenTarget != target || !saved.FutureSetting["keep"] {
			t.Fatalf("configuration lost settings: %s, %v", data, err)
		}
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("configuration permissions: %v, %v", info, err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("config set touched read state: %v", err)
	}
}

func TestConfigSetCreatesFileAndSupportsJSON(t *testing.T) {
	configPath, _ := configPaths(t)
	for _, args := range [][]string{
		{"config", "set", "message_open_target", "browser", "--json"},
		{"config", "get", "message_open_target", "--json"},
	} {
		out, err := run(t, args...)
		var value map[string]string
		if err != nil || json.Unmarshal([]byte(out), &value) != nil || len(value) != 1 || value["message_open_target"] != "browser" {
			t.Fatalf("JSON output = %q, %v", out, err)
		}
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config set did not create the config file: %v", err)
	}
}

func TestConfigRejectsInvalidInputWithoutWriting(t *testing.T) {
	for _, args := range [][]string{
		{"config", "get"},
		{"config", "get", "message_open_target", "extra"},
		{"config", "get", "unknown"},
		{"config", "set"},
		{"config", "set", "message_open_target"},
		{"config", "set", "message_open_target", "browser", "extra"},
		{"config", "set", "unknown", "browser"},
		{"config", "set", "message_open_target", "both"},
		{"config", "set", "message_open_target", ""},
		{"config", "unknown"},
		{"--demo", "config", "get", "message_open_target"},
		{"--demo", "config", "set", "message_open_target", "browser"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			configPath, _ := configPaths(t)
			if out, err := run(t, args...); err == nil {
				t.Fatalf("invalid command accepted: %q", out)
			}
			if _, err := os.Stat(filepath.Dir(configPath)); !os.IsNotExist(err) {
				t.Fatalf("invalid command touched configuration: %v", err)
			}
		})
	}
}

func TestConfigSetPreservesMalformedFiles(t *testing.T) {
	for _, content := range []string{`{"sidebar_mode":`, `null`, `[]`} {
		t.Run(content, func(t *testing.T) {
			configPath, _ := configPaths(t)
			if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := run(t, "config", "set", "message_open_target", "browser"); err == nil {
				t.Fatalf("malformed file accepted: %q", out)
			}
			data, err := os.ReadFile(configPath)
			if err != nil || string(data) != content {
				t.Fatalf("malformed configuration changed: %q, %v", data, err)
			}
		})
	}
}

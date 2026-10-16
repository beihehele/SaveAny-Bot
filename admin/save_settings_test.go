package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/rule"
)

func TestSaveSettingsCRUDAndOwnership(t *testing.T) {
	const env = "SAVEANY_SAVE_SETTINGS_TEST"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestSaveSettingsCRUDAndOwnership$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated save settings: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	cfg := `[admin]
enable=true
password='isolated-test-password'
[[storages]]
name='本地存储'
type='local'
enable=true
base_path='downloads'
[[storages]]
name='other'
type='local'
enable=true
base_path='other'
[[storages]]
name='ShardReel'
type='shardreel'
enable=true
url='http://127.0.0.1:1'
api_key='isolated-save-settings-key-2026-test'
[[users]]
id=42
storages=['本地存储','other','ShardReel']
[[users]]
id=43
storages=['本地存储']
`
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	s := testServer(t)
	cookie, csrf := signIn(t, s)
	send := func(method, endpoint string, body any, want int) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response := perform(s, method, "/admin/api/v1/"+endpoint, string(data), cookie, csrf, "http://console.test")
		if response.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, endpoint, response.Code, want, response.Body.String())
		}
	}
	for _, route := range []struct{ method, path string }{{"POST", "directories"}, {"PUT", "directories/1"}, {"DELETE", "directories/1"}, {"POST", "rules"}, {"PUT", "rules/1"}, {"DELETE", "rules/1"}} {
		for _, auth := range []struct {
			cookie       *http.Cookie
			csrf, origin string
			want         int
		}{{nil, "", "", 401}, {cookie, "", "http://console.test", 403}, {cookie, csrf, "http://foreign.test", 403}} {
			response := perform(s, route.method, "/admin/api/v1/"+route.path, `{"user_chat_id":42}`, auth.cookie, auth.csrf, auth.origin)
			if response.Code != auth.want {
				t.Fatalf("unverified %s %s: %d", route.method, route.path, response.Code)
			}
		}
	}
	directory := map[string]any{"user_chat_id": 42, "storage": "本地存储", "path": "/收件箱//视频/"}
	send("POST", "directories", directory, 200)
	user, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil || len(user.Dirs) != 1 || user.Dirs[0].Path != "收件箱/视频" {
		t.Fatalf("directory not normalized: %+v %v", user, err)
	}
	id := user.Dirs[0].ID
	endpoint := fmt.Sprintf("directories/%d", id)
	send("PATCH", "preferences", map[string]any{"user_chat_id": 42, "default_storage": "本地存储", "default_dir": id}, 200)
	if err := os.MkdirAll("downloads/收件箱/视频", 0700); err != nil {
		t.Fatal(err)
	}
	file := "downloads/收件箱/视频/keep.bin"
	if err := os.WriteFile(file, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	directory["path"] = "新的目录"
	send("PUT", endpoint, directory, 200)
	user, err = database.GetUserByChatID(t.Context(), 42)
	if err != nil || user.DefaultDir != id || user.Dirs[0].ID != id || user.Dirs[0].Path != "新的目录" {
		t.Fatalf("edit changed identity/default: %+v %v", user, err)
	}
	directory["user_chat_id"] = 43
	send("PUT", endpoint, directory, 404)
	send("DELETE", endpoint, map[string]any{"user_chat_id": 43}, 404)
	directory["user_chat_id"] = 42
	directory["storage"] = "other"
	send("PUT", endpoint, directory, 200)
	user, err = database.GetUserByChatID(t.Context(), 42)
	if err != nil || user.DefaultDir != 0 || user.DefaultStorage != "本地存储" {
		t.Fatalf("cross-storage edit left stale default: %+v %v", user, err)
	}
	directory["storage"] = "本地存储"
	send("PUT", endpoint, directory, 200)
	send("PATCH", "preferences", map[string]any{"user_chat_id": 42, "default_dir": id}, 200)
	send("DELETE", endpoint, map[string]any{"user_chat_id": 42}, 200)
	send("DELETE", endpoint, map[string]any{"user_chat_id": 42}, 404)
	user, err = database.GetUserByChatID(t.Context(), 42)
	if err != nil || user.DefaultDir != 0 || len(user.Dirs) != 0 {
		t.Fatalf("delete left dangling default: %+v %v", user, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "preserved" {
		t.Fatal("shortcut mutation changed files", err)
	}
	for _, bad := range []string{"", "/", "../escape", "a/../b", "//server/share", `C:\escape`, "bad\npath", "bad\x00path"} {
		directory["path"] = bad
		send("POST", "directories", directory, 400)
	}
	directory["path"] = "inbox"
	directory["storage"] = "missing"
	send("POST", "directories", directory, 400)
	directory["user_chat_id"] = 43
	directory["storage"] = "other"
	send("POST", "directories", directory, 400)
	directory["user_chat_id"] = 999
	directory["storage"] = "本地存储"
	send("POST", "directories", directory, 400)
	for _, path := range []string{"directories/0", "directories/no-id", "rules/0", "rules/no-id"} {
		send("PUT", path, map[string]any{"user_chat_id": 42}, 400)
	}
	entry := map[string]any{"user_chat_id": 42, "type": "MESSAGE-REGEX", "data": "旅行|风景", "storage": "CHOSEN", "path": ""}
	send("POST", "rules", entry, 200)
	user, err = database.GetUserByChatID(t.Context(), 42)
	if err != nil || len(user.Rules) != 1 || user.ApplyRule {
		t.Fatalf("rule changed enable preference: %+v %v", user, err)
	}
	first := user.Rules[0]
	ruleEndpoint := fmt.Sprintf("rules/%d", first.ID)
	entry["data"] = "出游"
	entry["storage"] = "ShardReel"
	entry["path"] = "2026/合集:一"
	send("PUT", ruleEndpoint, entry, 200)
	user, err = database.GetUserByChatID(t.Context(), 42)
	if err != nil || user.Rules[0].ID != first.ID || !user.Rules[0].CreatedAt.Equal(first.CreatedAt) || user.Rules[0].Data != "出游" || user.Rules[0].DirPath != "2026/合集:一" {
		t.Fatalf("rule edit lost settings or identity: %+v %v", user, err)
	}
	entry["user_chat_id"] = 43
	entry["storage"] = "本地存储"
	entry["path"] = ""
	send("PUT", ruleEndpoint, entry, 404)
	send("DELETE", ruleEndpoint, map[string]any{"user_chat_id": 43}, 404)
	entry["user_chat_id"] = 42
	for _, bad := range []struct{ kind, data string }{{"unknown", "x"}, {"MESSAGE-REGEX", "("}, {"FILENAME-REGEX", ""}, {"IS-ALBUM", "yes"}} {
		entry["type"] = bad.kind
		entry["data"] = bad.data
		send("PUT", ruleEndpoint, entry, 400)
	}
	entry["type"] = "IS-ALBUM"
	entry["data"] = "false"
	entry["storage"] = "CHOSEN"
	entry["path"] = "NEW-FOR-ALBUM"
	send("PUT", ruleEndpoint, entry, 200)
	entry["type"] = "FILENAME-REGEX"
	entry["data"] = rule.PresetCategories("")[0].Regex
	entry["path"] = "视频"
	send("PUT", ruleEndpoint, entry, 200)
	response := perform(s, "GET", "/admin/api/v1/preferences", "", cookie, "", "")
	var view struct {
		Presets []struct{ Name, Data string } `json:"rule_presets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || len(view.Presets) != 5 || view.Presets[0].Data != entry["data"] {
		t.Fatalf("missing shared presets: %s %v", response.Body.String(), err)
	}
	send("DELETE", ruleEndpoint, map[string]any{"user_chat_id": 42}, 200)
	user, err = database.GetUserByChatID(t.Context(), 42)
	if err != nil || len(user.Rules) != 0 || user.ApplyRule || user.DefaultStorage != "本地存储" {
		t.Fatalf("rule removal changed preferences: %+v %v", user, err)
	}

	t.Run("editable expressions round trip without changing matching", func(t *testing.T) {
		for _, tc := range []struct{ expression, sample, display string }{
			{`https://example.com/(video|photo)`, "https://example.com/video", `https://example.com/(video|photo)`},
			{`https://example.com/item[?]kind=video`, "https://example.com/item?kind=video", `https://example.com/item[?]kind=video`},
			{`https://example\.com/.*`, "https://example.com/video", `https://example\.com/.*`},
			{`(?i)isolated-test-password`, "ISOLATED-TEST-PASSWORD", `(?i)[redacted]`},
		} {
			entry := map[string]any{"user_chat_id": 42, "type": "MESSAGE-REGEX", "data": tc.expression, "storage": "CHOSEN", "path": "original"}
			send("POST", "rules", entry, 200)
			user, err := database.GetUserByChatID(t.Context(), 42)
			if err != nil || len(user.Rules) != 1 {
				t.Fatalf("create expression: %+v %v", user, err)
			}
			id := user.Rules[0].ID
			response := perform(s, "GET", "/admin/api/v1/preferences", "", cookie, "", "")
			var view struct{ Users []userPreferences }
			if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			var displayed string
			for _, user := range view.Users {
				for _, rule := range user.Rules {
					if rule.ID == id {
						displayed = rule.Data
					}
				}
			}
			if displayed != tc.display || strings.Contains(response.Body.String(), "isolated-test-password") {
				t.Fatalf("expression changed or credential exposed: %q", displayed)
			}
			// The editor omits an unchanged expression, including a redacted one.
			delete(entry, "data")
			entry["path"] = "edited"
			endpoint := fmt.Sprintf("rules/%d", id)
			send("PUT", endpoint, entry, 200)
			user, err = database.GetUserByChatID(t.Context(), 42)
			if err != nil || user.Rules[0].Data != tc.expression || user.Rules[0].DirPath != "edited" || !regexp.MustCompile(user.Rules[0].Data).MatchString(tc.sample) {
				t.Fatalf("directory edit changed matching: %+v %v", user, err)
			}
			entry["type"] = "IS-ALBUM"
			send("PUT", endpoint, entry, 400) // A new type needs its own explicit data.
			entry["type"] = "MESSAGE-REGEX"
			entry["data"] = "新表达式"
			send("PUT", endpoint, entry, 200)
			user, err = database.GetUserByChatID(t.Context(), 42)
			if err != nil || user.Rules[0].Data != "新表达式" {
				t.Fatalf("explicit expression edit lost: %+v %v", user, err)
			}
			send("DELETE", endpoint, map[string]any{"user_chat_id": 42}, 200)
		}
		send("POST", "rules", map[string]any{"user_chat_id": 42, "type": "MESSAGE-REGEX", "storage": "CHOSEN", "path": ""}, 400)
		send("PUT", "rules/4294967295", map[string]any{"user_chat_id": 42, "type": "MESSAGE-REGEX", "storage": "CHOSEN", "path": ""}, 404)
	})

	t.Run("ShardReel paths are checked before persisting settings", func(t *testing.T) {
		directory := map[string]any{"user_chat_id": 42, "storage": "ShardReel", "path": "有效目录"}
		entry := map[string]any{"user_chat_id": 42, "type": "MESSAGE-REGEX", "data": ".*", "storage": "ShardReel", "path": "有效目录"}
		send("POST", "directories", directory, 200)
		send("POST", "rules", entry, 200)
		user, err := database.GetUserByChatID(t.Context(), 42)
		if err != nil || len(user.Dirs) != 1 || len(user.Rules) != 1 {
			t.Fatalf("create path fixtures: %+v %v", user, err)
		}
		dirEndpoint, ruleEndpoint := fmt.Sprintf("directories/%d", user.Dirs[0].ID), fmt.Sprintf("rules/%d", user.Rules[0].ID)
		for _, bad := range []string{"归档/ 视频", "归档 /视频", "归档/\u00a0视频", strings.Repeat("a/", 1025) + "z", strings.Repeat("中/", 512) + "z"} {
			directory["path"], entry["path"] = bad, bad
			send("POST", "directories", directory, 400)
			send("PUT", dirEndpoint, directory, 400)
			for _, name := range []string{"ShardReel", "CHOSEN"} {
				entry["storage"] = name
				send("POST", "rules", entry, 400)
				send("PUT", ruleEndpoint, entry, 400)
			}
		}
		user, err = database.GetUserByChatID(t.Context(), 42)
		if err != nil || len(user.Dirs) != 1 || user.Dirs[0].Path != "有效目录" || len(user.Rules) != 1 || user.Rules[0].DirPath != "有效目录" {
			t.Fatalf("rejected paths changed saved settings: %+v %v", user, err)
		}
		entry["storage"], entry["path"] = "ShardReel", ""
		send("PUT", ruleEndpoint, entry, 200) // Rules can still select the storage root.
		directory["path"] = "/归档//合集:一/"
		send("PUT", dirEndpoint, directory, 200)
		user, err = database.GetUserByChatID(t.Context(), 42)
		if err != nil || user.Dirs[0].Path != "归档/合集:一" {
			t.Fatalf("valid remote path was not normalized: %+v %v", user, err)
		}
		// A local-only user's CHOSEN rule keeps the local backend's valid paths.
		entry["user_chat_id"], entry["storage"], entry["path"] = 43, "CHOSEN", "归档/ 视频"
		send("POST", "rules", entry, 200)
		user, err = database.GetUserByChatID(t.Context(), 43)
		if err != nil || len(user.Rules) != 1 || user.Rules[0].DirPath != "归档/ 视频" {
			t.Fatalf("local-only rule changed: %+v %v", user, err)
		}
		send("DELETE", fmt.Sprintf("rules/%d", user.Rules[0].ID), map[string]any{"user_chat_id": 42}, 404)
		delete(entry, "data")
		send("PUT", ruleEndpoint, entry, 404)
	})
}

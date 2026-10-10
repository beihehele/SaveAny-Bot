package database

import (
	"bytes"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/celestix/gotgproto/sessionMaker"

	"github.com/krau/SaveAny-Bot/config"
)

// This rehearses a stopped, synthetic deployment. It never opens a production
// database, authenticates to Telegram, or claims the old binary was exercised.
func TestStoppedDeploymentBackupUpgradeAndRestore(t *testing.T) {
	previous := db
	t.Cleanup(func() { db = previous })
	source, backup, upgraded, rollback := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		file := filepath.Join(source, relative)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.toml", "[[users]]\nid=42\nstorages=['archive']\n[[storages]]\nname='archive'\ntype='local'\nenable=true\nbase_path='downloads'\n")
	write("private/notes.txt", "private deployment fixture\n")
	write("downloads/saved.txt", "previously saved file\n")
	start := func(root string) {
		t.Helper()
		t.Setenv("SAVEANY_DB_PATH", filepath.Join(root, "data", "saveany.db"))
		t.Setenv("SAVEANY_DB_SESSION", filepath.Join(root, "data", "session.db"))
		t.Setenv("SAVEANY_TELEGRAM_USERBOT_SESSION", filepath.Join(root, "data", "usersession.db"))
		if err := config.Init(t.Context(), filepath.Join(root, "config.toml")); err != nil {
			t.Fatal(err)
		}
		Init(t.Context())
		if err := ValidateStorageReferences(t.Context()); err != nil {
			t.Fatal(err)
		}
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	stop := func() {
		t.Helper()
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	start(source)
	user, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	dir := Dir{UserID: user.ID, StorageName: "archive", Path: "albums"}
	if err := db.WithContext(t.Context()).Create(&dir).Error; err != nil {
		t.Fatal(err)
	}
	user.DefaultStorage, user.DefaultDir, user.Silent, user.ApplyRule = "archive", dir.ID, true, true
	user.FilenameStrategy, user.FilenameTemplate, user.ConflictStrategy = "TEMPLATE", "{{.Message}}", "OVERWRITE"
	if err := UpdateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	rule := Rule{UserID: user.ID, Type: "IS-ALBUM", Data: "true", StorageName: "archive", DirPath: "NEW-FOR-ALBUM"}
	route := WatchChat{UserID: user.ID, ChatID: 100, TargetID: 200, TargetTopicID: 300,
		SourceName: "source", TargetName: "destination", TargetTopicName: "topic", Filter: "caption"}
	for _, model := range []any{&rule, &route} {
		if err := db.WithContext(t.Context()).Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("DROP INDEX idx_watch_route").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_watch_route ON watch_chats(user_id, chat_id, target_id)").Error; err != nil {
		t.Fatal(err)
	}
	want, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	for _, filename := range []string{"session.db", "usersession.db"} {
		peer, session, err := sessionMaker.NewSessionStorage(t.Context(), sessionMaker.SqlSession(GetDialect(filepath.Join(source, "data", filename))), false)
		if err != nil {
			t.Fatal(err)
		}
		data := []byte("synthetic-session-" + filename)
		if err := session.StoreSession(t.Context(), data); err != nil {
			t.Fatal(err)
		}
		if got := peer.GetSession().Data; !bytes.Equal(got, data) {
			t.Fatal("session fixture was not persisted")
		}
		conn, err := peer.SqlSession.DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	manifest := deploymentFileHashes(t, source)
	if err := os.CopyFS(backup, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	assertFiles := func(root string) {
		t.Helper()
		if !reflect.DeepEqual(deploymentFileHashes(t, root), manifest) {
			t.Fatal("backup/restore changed or omitted deployment files")
		}
	}
	assertFiles(backup)
	if err := os.CopyFS(upgraded, os.DirFS(backup)); err != nil {
		t.Fatal(err)
	}
	assertFiles(upgraded)
	verify := func(root string) {
		t.Helper()
		got, err := GetUserByChatID(t.Context(), 42)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("restored preferences or directory/rule/topic associations changed: err=%v got=%+v want=%+v", err, got, want)
		}
		var integrity string
		if err := db.Raw("PRAGMA integrity_check").Scan(&integrity).Error; err != nil || integrity != "ok" {
			t.Fatalf("restored database integrity: %q %v", integrity, err)
		}
		for _, filename := range []string{"session.db", "usersession.db"} {
			peer, session, err := sessionMaker.NewSessionStorage(t.Context(), sessionMaker.SqlSession(GetDialect(filepath.Join(root, "data", filename))), false)
			if err != nil {
				t.Fatal(err)
			}
			data, err := session.LoadSession(t.Context())
			if err != nil || !bytes.Equal(data, []byte("synthetic-session-"+filename)) {
				t.Fatalf("restored %s session changed: %v", filename, err)
			}
			conn, err := peer.SqlSession.DB()
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	start(upgraded)
	verify(upgraded)
	var columns []struct{ Name string }
	if err := db.Raw("PRAGMA index_info('idx_watch_route')").Scan(&columns).Error; err != nil {
		t.Fatalf("upgrade failed to migrate the route index: %+v %v", columns, err)
	}
	names := make([]string, len(columns))
	for i, column := range columns {
		names[i] = column.Name
	}
	if !reflect.DeepEqual(names, []string{"user_id", "chat_id", "target_id", "target_topic_id"}) {
		t.Fatalf("upgraded route index has incorrect columns: %v", names)
	}
	// Keep the modified deployment separate; rollback uses the entire original
	// stopped snapshot rather than reusing or deleting the upgraded database.
	if err := db.Model(&User{}).Where("chat_id = ?", 42).Update("silent", false).Error; err != nil {
		t.Fatal(err)
	}
	stop()
	if err := os.CopyFS(rollback, os.DirFS(backup)); err != nil {
		t.Fatal(err)
	}
	assertFiles(rollback)
	start(rollback)
	verify(rollback)
	stop()
	assertFiles(backup)
	assertFiles(source)
}

func deploymentFileHashes(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	hashes := make(map[string][sha256.Size]byte)
	if err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		hashes[name] = sha256.Sum256(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return hashes
}

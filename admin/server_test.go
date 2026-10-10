package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	filemodel "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/spf13/viper"

	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/storage"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(t.Context(), config.AdminConfig{Enable: true, Host: "127.0.0.1", Port: 18081,
		Password: "isolated-test-password", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func perform(s *Server, method, endpoint, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://console.test"+endpoint, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, request)
	return recorder
}

func signIn(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	response := perform(s, "POST", "/admin/api/v1/login", `{"username":"admin","password":"isolated-test-password"}`, nil, "", "http://console.test")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var entry adminSession
	if err := json.Unmarshal(response.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	return response.Result().Cookies()[0], entry.CSRF
}

func TestAuthenticationCSRFAndSessionLifecycle(t *testing.T) {
	s := testServer(t)
	for _, endpoint := range []string{"session", "overview", "tasks", "storages", "task-types", "files", "download", "preferences", "diagnostics"} {
		if response := perform(s, "GET", "/admin/api/v1/"+endpoint, "", nil, "", ""); response.Code != 401 {
			t.Fatal("unauthenticated route", endpoint, response.Code)
		}
	}
	if response := perform(s, "POST", "/admin/api/v1/login", `{}`, nil, "", "https://attacker.test"); response.Code != 403 {
		t.Fatal("cross-site login accepted")
	}
	if response := perform(s, "POST", "/admin/api/v1/login", `{"username":"admin","password":"bad"}`, nil, "", "http://console.test"); response.Code != 401 {
		t.Fatal("wrong password accepted")
	}
	cookie, csrf := signIn(t, s)
	if !cookie.HttpOnly || cookie.Path != "/admin/" || cookie.SameSite != http.SameSiteStrictMode || len(cookie.Value) != 64 || strings.Contains(cookie.Value, "password") {
		t.Fatal("unsafe session cookie", cookie)
	}
	if response := perform(s, "GET", "/admin/api/v1/session", "", cookie, "", ""); response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session read failed")
	}
	for _, input := range []struct{ csrf, origin string }{{"", "http://console.test"}, {csrf, "http://attacker.test"}, {csrf, ""}, {"wrong", "http://console.test"}} {
		if response := perform(s, "POST", "/admin/api/v1/logout", "", cookie, input.csrf, input.origin); response.Code != 403 {
			t.Fatal("unverified write accepted")
		}
	}
	s.auth.cleanup(time.Now().Add(2 * time.Hour))
	if response := perform(s, "GET", "/admin/api/v1/session", "", cookie, "", ""); response.Code != 401 {
		t.Fatal("expired session accepted")
	}
	cookie, csrf = signIn(t, s)
	if response := perform(s, "POST", "/admin/api/v1/logout", "", cookie, csrf, "http://console.test"); response.Code != 200 || response.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not expire cookie")
	}
	if response := perform(s, "GET", "/admin/api/v1/session", "", cookie, "", ""); response.Code != 401 {
		t.Fatal("revoked session accepted")
	}
	fresh := testServer(t)
	if response := perform(fresh, "GET", "/admin/api/v1/session", "", cookie, "", ""); response.Code != 401 {
		t.Fatal("old session accepted by restarted server")
	}
}

func TestLoginBoundsAndSecureCookie(t *testing.T) {
	s := testServer(t)
	for i := 0; i < 5; i++ {
		if response := perform(s, "POST", "/admin/api/v1/login", `{"username":"admin","password":"bad"}`, nil, "", "http://console.test"); response.Code != 401 {
			t.Fatal("unexpected failed login status", response.Code)
		}
	}
	if response := perform(s, "POST", "/admin/api/v1/login", `{}`, nil, "", "http://console.test"); response.Code != 429 || response.Header().Get("Retry-After") == "" {
		t.Fatal("login limit not enforced")
	}
	s.auth.cleanup(time.Now().Add(2 * time.Minute))
	s.cfg.SecureCookie = true
	response := perform(s, "POST", "/admin/api/v1/login", `{"username":"admin","password":"isolated-test-password"}`, nil, "", "https://console.test")
	if response.Code != 200 || !response.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie policy not enforced")
	}
	for i := 0; i < 30; i++ {
		if _, _, err := s.auth.issue(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.auth.sessions) != 16 {
		t.Fatal("session map is not bounded")
	}
	for i := 0; i < 1100; i++ {
		s.auth.allowed(fmt.Sprint(i), time.Now())
	}
	if len(s.auth.attempts) > 1024 {
		t.Fatal("login table is not bounded")
	}
}

type waitingTask struct{ id, title string }

func (task waitingTask) TaskID() string          { return task.id }
func (task waitingTask) Title() string           { return task.title }
func (task waitingTask) Type() tasktype.TaskType { return tasktype.TaskTypeTgfiles }
func (task waitingTask) Execute(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

type heldCancellationTask struct {
	started chan struct{}
	release chan struct{}
}

func (heldCancellationTask) TaskID() string          { return "console-held-cancellation" }
func (heldCancellationTask) Title() string           { return "Held cancellation" }
func (heldCancellationTask) Type() tasktype.TaskType { return tasktype.TaskTypeTgfiles }
func (task heldCancellationTask) Execute(ctx context.Context) error {
	close(task.started)
	<-ctx.Done()
	<-task.release
	return ctx.Err()
}

func TestConsoleTaskFilesAndReadOnlyPreferences(t *testing.T) {
	const helperEnv = "SAVEANY_ADMIN_TEST_ROOT"
	if os.Getenv(helperEnv) == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestConsoleTaskFilesAndReadOnlyPreferences$", "-test.count=1")
		command.Env = append(os.Environ(), helperEnv+"="+t.TempDir())
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated console integration: %v\n%s", err, output)
		}
		return
	}
	root := os.Getenv(helperEnv)
	t.Chdir(root)
	viper.Reset()
	t.Cleanup(viper.Reset)
	configuration := `workers=1
[telegram]
token='0:private-test-bot-token'
[admin]
enable=true
password='isolated-test-password'
[api]
enable=false
token='private-test-api-token'
[[storages]]
name='console-local'
type='local'
enable=true
base_path='.'
[[users]]
id=42
blacklist=true
`
	if err := os.WriteFile("config.toml", []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("sample.txt", []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), "config.toml"); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	storage.LoadStorages(t.Context())
	user, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := user.WatchChat(t.Context(), database.WatchChat{UserID: user.ID, ChatID: 100, TargetID: 200, TargetTopicID: 300, Filter: "caption"}); err != nil {
		t.Fatal(err)
	}
	before, err := database.GetAllUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	core.Prepare()
	s, err := NewServer(t.Context(), config.C().Admin)
	if err != nil {
		t.Fatal(err)
	}
	s.factory = api.NewTaskFactoryWithExtractor(t.Context(), func(ctx context.Context, links []string) ([]filemodel.TGFileMessage, error) {
		file, err := filemodel.FromMediaMessage(&tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "fixture.txt"}}}}, consoleDownloadClient{}, &tg.Message{ID: 1})
		return []filemodel.TGFileMessage{file}, err
	})
	cookie, csrf := signIn(t, s)
	if err := core.AddTask(t.Context(), waitingTask{"console-bot-task", "<img src=x onerror=alert(1)>"}); err != nil {
		t.Fatal(err)
	}
	defer core.CancelTask(t.Context(), "console-bot-task")
	response := perform(s, "GET", "/admin/api/v1/tasks", "", cookie, "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "console-bot-task") || !strings.Contains(response.Body.String(), `"scope":"queue"`) {
		t.Fatal("bot queue task missing", response.Body.String())
	}
	response = perform(s, "POST", "/admin/api/v1/tasks", `{"type":"tgfiles","storage":"console-local","path":"fixture.txt","params":{"message_links":["https://t.me/c/123/1"]}}`, cookie, csrf, "http://console.test")
	if response.Code != 201 {
		t.Fatal("web submission failed", response.Body.String())
	}
	var submitted api.CreateTaskResponse
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { api.DeleteTask(submitted.TaskID) })
	if response := perform(s, "POST", "/admin/api/v1/tasks/"+submitted.TaskID+"/cancel", "", cookie, csrf, "http://console.test"); response.Code != 200 {
		t.Fatal("web cancellation failed", response.Body.String())
	}
	if response := perform(s, "POST", "/admin/api/v1/tasks/console-bot-task/cancel", "", cookie, csrf, "http://console.test"); response.Code != 200 {
		t.Fatal("bot task cancellation failed")
	}
	for _, endpoint := range []string{"overview", "storages", "task-types", "preferences", "diagnostics", "files?storage=console-local&path=/"} {
		response := perform(s, "GET", "/admin/api/v1/"+endpoint, "", cookie, "", "")
		if response.Code != 200 {
			t.Fatal(endpoint, response.Code, response.Body.String())
		}
		for _, secret := range []string{"private-test-bot-token", "private-test-api-token", "isolated-test-password", configuration} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("response leaked credentials", endpoint)
			}
		}
	}
	if response := perform(s, "GET", "/admin/api/v1/download?storage=console-local&path=config.toml", "", cookie, "", ""); response.Code != 403 {
		t.Fatal("config download allowed")
	}
	if response := perform(s, "GET", "/admin/api/v1/download?storage=console-local&path=data/saveany.db", "", cookie, "", ""); response.Code != 403 {
		t.Fatal("database download allowed")
	}
	if response := perform(s, "GET", "/admin/api/v1/download?storage=console-local&path=../outside", "", cookie, "", ""); response.Code != 400 {
		t.Fatal("parent traversal allowed")
	}
	if err := os.Link(filepath.Join(root, "config.toml"), filepath.Join(root, "alias.txt")); err == nil {
		if response := perform(s, "GET", "/admin/api/v1/download?storage=console-local&path=alias.txt", "", cookie, "", ""); response.Code != 403 {
			t.Fatal("hardlinked config download allowed")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "data", "session.db-wal"), []byte("private sidecar fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if response := perform(s, "GET", "/admin/api/v1/download?storage=console-local&path=data/session.db-wal", "", cookie, "", ""); response.Code != 403 {
		t.Fatal("database sidecar download allowed")
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside storage fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, destination := range map[string]string{"config-link.txt": filepath.Join(root, "config.toml"), "outside-link.txt": outside} {
		if err := os.Symlink(destination, filepath.Join(root, name)); err != nil {
			t.Logf("symlink test unavailable on this platform: %v", err)
			continue
		}
		if response := perform(s, "GET", "/admin/api/v1/download?storage=console-local&path="+name, "", cookie, "", ""); response.Code < 400 {
			t.Fatal("sensitive or escaping symlink download allowed", name)
		}
	}
	request := httptest.NewRequest("GET", "http://console.test/admin/api/v1/download?storage=console-local&path=sample.txt", nil)
	request.AddCookie(cookie)
	request.Header.Set("Range", "bytes=2-5")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, request)
	if recorder.Code != 206 || recorder.Body.String() != "2345" || !strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal("range download failed", recorder.Code, recorder.Body.String())
	}
	apiAuth := api.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	apiRequest := httptest.NewRequest("GET", "http://console.test/api/v1/tasks", nil)
	apiRequest.AddCookie(cookie)
	apiResponse := httptest.NewRecorder()
	apiAuth.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != 401 {
		t.Fatal("admin cookie was accepted as API token")
	}
	apiRequest.Header.Set("Authorization", "Bearer private-test-api-token")
	apiResponse = httptest.NewRecorder()
	apiAuth.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != 200 {
		t.Fatal("existing API authentication changed")
	}
	adminRequest := httptest.NewRequest("GET", "http://console.test/admin/api/v1/tasks", nil)
	adminRequest.Header.Set("Authorization", "Bearer private-test-api-token")
	adminResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != 401 {
		t.Fatal("API token was accepted as admin session")
	}
	if response := perform(s, "POST", "/admin/api/v1/preferences", `{}`, cookie, csrf, "http://console.test"); response.Code != 405 {
		t.Fatal("preferences unexpectedly writable")
	}
	after, err := database.GetAllUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("console changed preferences/routes")
	}
	// Cancellation must retain executing state until the worker really finishes.
	workerCtx, stopWorkers := context.WithCancel(t.Context())
	task := heldCancellationTask{make(chan struct{}), make(chan struct{})}
	if err := core.AddTask(workerCtx, task); err != nil {
		t.Fatal(err)
	}
	done := core.Run(workerCtx)
	t.Cleanup(func() {
		stopWorkers()
		select {
		case <-task.release:
		default:
			close(task.release)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("workers did not stop")
		}
	})
	select {
	case <-task.started:
	case <-time.After(5 * time.Second):
		t.Fatal("held task did not start")
	}
	response = perform(s, "POST", "/admin/api/v1/tasks/"+task.TaskID()+"/cancel", "", cookie, csrf, "http://console.test")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"cancelling"`) {
		t.Fatal("running cancellation reported completion too early", response.Body.String())
	}
	var held taskRow
	for _, row := range s.taskRows(t.Context()) {
		if row.TaskID == task.TaskID() {
			held = row
		}
	}
	if !held.Executing || held.CanCancel || held.Status != "cancelling" {
		t.Fatalf("invalid held cancellation state: %+v", held)
	}
	close(task.release)
	deadline := time.Now().Add(5 * time.Second)
	for core.IsTaskExecuting(task.TaskID()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if core.IsTaskExecuting(task.TaskID()) {
		t.Fatal("finished task retained execution state")
	}
}

func TestRedactionAndStaticResources(t *testing.T) {
	s := testServer(t)
	s.secrets = []string{"private-credential"}
	text := s.redact("private-credential https://user:pass@example.test/file?token=secret#fragment")
	for _, secret := range []string{"private-credential", "user:pass", "token=secret", "fragment"} {
		if strings.Contains(text, secret) {
			t.Fatal("unredacted secret", text)
		}
	}
	for _, resource := range []string{"/admin/", "/admin/assets/app.js", "/admin/assets/style.css", "/admin/assets/locales/en.json", "/admin/assets/locales/zh-Hans.json"} {
		if response := perform(s, "GET", resource, "", nil, "", ""); response.Code != 200 || response.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("static page not available", resource, response.Code)
		}
	}
	if response := perform(s, "GET", "/admin/assets/server.go", "", nil, "", ""); response.Code != 404 {
		t.Fatal("unexpected static resource exposed")
	}
	for i := 0; i < 130; i++ {
		s.audit.add("test", fmt.Sprint(i), "ok")
	}
	if entries := s.audit.snapshot(); len(entries) != 100 || entries[0].Object != "129" || entries[99].Object != "30" {
		t.Fatal("audit bound/order failed")
	}
}

// consoleDownloadClient mocks Telegram RPC without replacing the local-save task.
type consoleDownloadClient struct{ downloader.Client }

func (consoleDownloadClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data := []byte("data")
	if req.Offset >= int64(len(data)) {
		return &tg.UploadFile{}, nil
	}
	return &tg.UploadFile{Bytes: data[req.Offset:]}, nil
}

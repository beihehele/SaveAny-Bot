package admin

import (
	"fmt"
	"testing"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/taskcontrol"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
)

func TestAdminQueuedCopyCancellationReleasesOwnership(t *testing.T) {
	core.Prepare()
	s := testServer(t)
	cookie, csrf := signIn(t, s)
	id := fmt.Sprintf("admin-copy-%s", t.Name())
	const user int64 = 91004
	if !copyfwd.TryBegin(user, id) {
		t.Fatal("cannot acquire slot")
	}
	t.Cleanup(func() { copyfwd.End(user, id); copyfwd.End(user, id+"-next") })
	task := copyfwd.NewTask(id, user, 100, 200, 0, "", 1, 0, 0, nil)
	if err := core.AddTask(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taskcontrol.CancelTask(t.Context(), id) })
	response := perform(s, "POST", "/admin/api/v1/tasks/"+id+"/cancel", "", cookie, csrf, "http://console.test")
	if response.Code != 200 {
		t.Fatalf("cancel failed: %d %s", response.Code, response.Body.String())
	}
	if !copyfwd.TryBegin(user, id+"-next") {
		t.Fatal("successful HTTP cancel left copy ownership behind")
	}
}

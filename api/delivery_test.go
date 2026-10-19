package api

import (
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestCollectionProgressAndReceiptsStayDetached(t *testing.T) {
	info := &TaskProgressInfo{Status: TaskStatusQueued}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseProgress, DeliveryProgress: &taskevent.DeliveryProgress{Stage: "uploading", Bytes: 3, TotalBytes: 4}})
	links := []string{"https://t.me/c/222/501"}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseProgress, DeliveryProgress: &taskevent.DeliveryProgress{Stage: "sent"}, ResultLinks: links})
	links[0] = "changed"
	r := convertTaskProgressToResponse(info)
	if r.DeliveryProgress.Stage != "sent" || r.ResultLinks[0] != "https://t.me/c/222/501" {
		t.Fatalf("bad collection response: %+v", r)
	}
	r.ResultLinks[0] = "changed"
	r.DeliveryProgress.Stage = "changed"
	r = convertTaskProgressToResponse(info)
	if r.DeliveryProgress.Stage != "sent" || r.ResultLinks[0] != "https://t.me/c/222/501" {
		t.Fatal("snapshot leaked mutable state")
	}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
	info.Emit(taskevent.Event{Phase: taskevent.PhaseProgress, ResultLinks: links})
	if len(convertTaskProgressToResponse(info).ResultLinks) != 1 {
		t.Fatal("late progress changed terminal record")
	}
}

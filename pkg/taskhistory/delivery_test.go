package taskhistory

import (
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestCollectionReceiptsAreDetachedAndDeletionStaysFinal(t *testing.T) {
	s := NewStore(10, time.Hour)
	ctx, _, _ := s.Track(t.Context(), "collection", "tgfiles", "TG收藏")
	progress := taskevent.DeliveryProgress{Stage: "sent"}
	links := []string{"https://t.me/c/222/501"}
	taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseProgress, DeliveryProgress: &progress, ResultLinks: links})
	links[0] = "changed"
	progress.Stage = "changed"
	r := s.Snapshots()[0]
	if r.ResultLinks[0] != "https://t.me/c/222/501" || r.DeliveryProgress.Stage != "sent" {
		t.Fatal("producer mutated receipt")
	}
	r.ResultLinks[0] = "changed"
	r.DeliveryProgress.Stage = "changed"
	r = s.Snapshots()[0]
	if r.ResultLinks[0] != "https://t.me/c/222/501" || r.DeliveryProgress.Stage != "sent" {
		t.Fatal("reader mutated receipt")
	}
	taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeCancelled})
	s.Delete("collection")
	taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseProgress, ResultLinks: links, DeliveryProgress: &progress})
	if len(s.Snapshots()) != 0 {
		t.Fatal("late receipt recreated deleted record")
	}
}

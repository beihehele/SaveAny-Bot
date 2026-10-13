package database

import "testing"

func TestCurrentWatchSchemaSeparatesTopicsAndRejectsDuplicateRoutes(t *testing.T) {
	db := testDatabase(t)
	for _, topic := range []int{3, 4} {
		if err := db.WithContext(t.Context()).Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 5, TargetTopicID: topic}).Error; err != nil {
			t.Fatalf("could not create current topic route: %v", err)
		}
	}
	if err := db.WithContext(t.Context()).Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 5, TargetTopicID: 3}).Error; err == nil {
		t.Fatal("duplicate current route accepted")
	}
}

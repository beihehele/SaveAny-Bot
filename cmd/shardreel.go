package cmd

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/client/bot"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

func startShardReel(ctx context.Context) (<-chan struct{}, error) {
	var stores []*shardreel.Store
	for _, backend := range storage.AllStorages() {
		if store, ok := backend.(*shardreel.Store); ok {
			if err := store.Recover(ctx); err != nil {
				return nil, err
			}
			stores = append(stores, store)
		}
	}
	notify := func(ctx context.Context, group database.ShardReelTask) error {
		if err := core.CompleteShardReel(ctx, group); err != nil {
			return err
		}
		var current int64
		if err := database.ShardReelDB(ctx).Model(&database.ShardReelTask{}).
			Where("id = ? AND result_version = ?", group.ID, group.ResultVersion).Count(&current).Error; err != nil {
			return err
		}
		if current == 0 {
			return nil
		}
		var summary taskresult.Summary
		if err := json.Unmarshal([]byte(group.Summary), &summary); err != nil {
			return err
		}
		taskhistory.Default.CompleteUpload(group.ID, group.Status, group.Error, summary.Counts())
		api.CompleteUpload(group)
		if group.ChatID != 0 && group.MessageID != 0 && bot.ExtContext() != nil {
			key := i18nk.BotMsgProgressShardreelCompleted
			if group.Status == "failed" {
				key = i18nk.BotMsgProgressShardreelFailed
			}
			if group.Status == "cancelled" {
				key = i18nk.BotMsgProgressShardreelCancelled
			}
			text := i18n.T(i18nk.BotMsgProgressShardreelResult, map[string]any{"Status": i18n.T(key), "Succeeded": summary.Succeeded,
				"Skipped": summary.Skipped, "Failed": summary.Failed + summary.Interrupted, "Cancelled": summary.Cancelled, "Error": group.Error})
			request := &tg.MessagesEditMessageRequest{ID: group.MessageID}
			request.SetMessage(group.Title + "\n" + text)
			request.SetEntities(nil)
			tgutil.EditProgress(tgutil.ExtWithContext(ctx, bot.ExtContext()), group.ChatID, request, true)
		}
		return nil
	}
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func() { defer wg.Done(); store.Run(ctx, notify) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	return done, nil
}

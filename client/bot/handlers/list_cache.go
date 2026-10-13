package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/krau/SaveAny-Bot/database"
)

// Refreshes are serialized so an older sync cannot overwrite a newer snapshot.
var listRefreshMu sync.Mutex

// ErrListSyncBusy prevents overlapping refreshes from replacing newer snapshots.
var ErrListSyncBusy = errors.New("Telegram list synchronization is already running")

// TopicChoice uses the stable Topic ID rather than its latest message ID.
type TopicChoice struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// ChatList is a complete cached chat snapshot with its synchronization time.
type ChatList struct {
	Chats     []ChatChoice `json:"chats"`
	UpdatedAt *time.Time   `json:"updated_at"`
}

// TopicList is one target's cached Topic snapshot.
type TopicList struct {
	Topics    []TopicChoice `json:"topics"`
	UpdatedAt *time.Time    `json:"updated_at"`
}

func (m ManagementService) listAccount(ctx context.Context, owner int64) (int64, error) {
	if _, err := ManagementUser(ctx, owner); err != nil {
		return 0, err
	}
	client, err := m.client(ctx)
	if err != nil {
		return 0, err
	}
	if client.Self == nil || client.Self.ID <= 0 {
		return 0, fmt.Errorf("userbot identity unavailable")
	}
	return client.Self.ID, nil
}

// CachedChats only reads metadata for the currently logged-in Userbot account.
func (m ManagementService) CachedChats(ctx context.Context, owner int64) (ChatList, error) {
	list := ChatList{Chats: make([]ChatChoice, 0)}
	account, err := m.listAccount(ctx, owner)
	if err != nil {
		return list, err
	}
	row, err := database.GetTelegramList(ctx, account, 0)
	if err != nil || row == nil {
		return list, err
	}
	if err := json.Unmarshal(row.Data, &list.Chats); err != nil {
		return list, fmt.Errorf("decode cached chats: %w", err)
	}
	list.UpdatedAt = &row.UpdatedAt
	return list, nil
}

// RefreshChats commits only after every normal and archived page succeeds.
func (m ManagementService) RefreshChats(ctx context.Context, owner int64) (ChatList, error) {
	list := ChatList{Chats: make([]ChatChoice, 0)}
	account, err := m.listAccount(ctx, owner)
	if err != nil {
		return list, err
	}
	if !listRefreshMu.TryLock() {
		return list, ErrListSyncBusy
	}
	defer listRefreshMu.Unlock()
	cursor := chatCursor{}
	seenCursors := make(map[chatCursor]bool)
	seenChats := make(map[int64]bool)
	for range 100 {
		page, err := m.chatsPage(ctx, owner, cursor)
		if err != nil {
			return list, err
		}
		for _, chat := range page.Chats {
			if !seenChats[chat.ID] {
				list.Chats = append(list.Chats, chat)
				seenChats[chat.ID] = true
			}
		}
		if len(list.Chats) > 10000 {
			return list, fmt.Errorf("chat list exceeds 10000 entries")
		}
		if page.Next == nil {
			data, err := json.Marshal(list.Chats)
			if err != nil {
				return list, err
			}
			var forums []int64
			for _, chat := range list.Chats {
				if chat.Managed && chat.Forum {
					forums = append(forums, chat.ID)
				}
			}
			updated, err := database.SaveTelegramList(ctx, account, 0, data, forums)
			list.UpdatedAt = &updated
			return list, err
		}
		if seenCursors[*page.Next] {
			return list, fmt.Errorf("chat cursor did not advance")
		}
		seenCursors[*page.Next] = true
		cursor = *page.Next
	}
	return list, fmt.Errorf("chat list page budget exceeded")
}

func topicCacheID(target string) (int64, error) {
	id, err := strconv.ParseInt(target, 10, 64)
	if err != nil || id >= 0 {
		return 0, fmt.Errorf("select a target chat")
	}
	return id, nil
}

// CachedTopics requires no forum lookup or Telegram RPC.
func (m ManagementService) CachedTopics(ctx context.Context, owner int64, target string) (TopicList, error) {
	list := TopicList{Topics: make([]TopicChoice, 0)}
	account, err := m.listAccount(ctx, owner)
	if err != nil {
		return list, err
	}
	id, err := topicCacheID(target)
	if err != nil {
		return list, err
	}
	row, err := database.GetTelegramList(ctx, account, id)
	if err != nil || row == nil {
		return list, err
	}
	if err := json.Unmarshal(row.Data, &list.Topics); err != nil {
		return list, fmt.Errorf("decode cached topics: %w", err)
	}
	list.UpdatedAt = &row.UpdatedAt
	return list, nil
}

// RefreshTopics replaces one target's snapshot after a complete, bounded scan.
func (m ManagementService) RefreshTopics(ctx context.Context, owner int64, target string) (TopicList, error) {
	list := TopicList{Topics: make([]TopicChoice, 0)}
	account, err := m.listAccount(ctx, owner)
	if err != nil {
		return list, err
	}
	id, err := topicCacheID(target)
	if err != nil {
		return list, err
	}
	if !listRefreshMu.TryLock() {
		return list, ErrListSyncBusy
	}
	defer listRefreshMu.Unlock()
	if err := m.ManagedTarget(ctx, owner, target); err != nil {
		return list, err
	}
	list.Topics, err = m.fetchTopics(ctx, owner, target)
	if err != nil {
		return list, err
	}
	data, err := json.Marshal(list.Topics)
	if err != nil {
		return list, err
	}
	updated, err := database.SaveTelegramList(ctx, account, id, data, nil)
	list.UpdatedAt = &updated
	return list, err
}

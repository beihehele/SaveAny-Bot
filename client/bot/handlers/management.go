package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"

	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/rs/xid"
)

// RouteRequest uses the same chat, Topic and filter syntax as /watch and /copy.
type RouteRequest struct {
	UserChatID int64  `json:"user_chat_id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Filter     string `json:"filter"`
	Count      int    `json:"count"`
}

// ManagedTarget checks current creator/admin membership for the target.
func (m ManagementService) ManagedTarget(ctx context.Context, owner int64, target string) error {
	if _, err := ManagementUser(ctx, owner); err != nil {
		return err
	}
	client, err := m.client(ctx)
	if err != nil {
		return err
	}
	pt, err := parseTargetWithTopic(target)
	if err != nil {
		return err
	}
	id, err := tgutil.ParseChatID(client, pt.ChatIDArg)
	if err != nil || id >= 0 {
		return fmt.Errorf("select a managed group or channel")
	}
	input, err := client.ResolveInputPeerById(id)
	if err != nil {
		return err
	}
	var chats []tg.ChatClass
	switch p := input.(type) {
	case *tg.InputPeerChannel:
		box, err := client.Raw.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}})
		if err != nil {
			return err
		}
		chats = box.GetChats()
	case *tg.InputPeerChat:
		box, err := client.Raw.MessagesGetChats(ctx, []int64{p.ChatID})
		if err != nil {
			return err
		}
		chats = box.GetChats()
	default:
		return fmt.Errorf("select a managed group or channel")
	}
	for _, raw := range chats {
		switch ch := raw.(type) {
		case *tg.Channel:
			_, admin := ch.GetAdminRights()
			if botAPIChannelPrefix-ch.ID == id && !ch.Left && (ch.Creator || admin) {
				return nil
			}
		case *tg.Chat:
			_, admin := ch.GetAdminRights()
			if -ch.ID == id && !ch.Left && !ch.Deactivated && (ch.Creator || admin) {
				return nil
			}
		}
	}
	return fmt.Errorf("target is no longer managed by the current Userbot account")
}

// ManagementService shares Bot validation, ownership and stop admission with
// the console. Request contexts own preparation; service contexts own tasks.
type ManagementService struct {
	Client func() *ext.Context
}

var ErrManagementConflict = errors.New("management operation conflicts with active work")

// ManagementUser restricts management to users configured for this instance.
func ManagementUser(ctx context.Context, chatID int64) (*database.User, error) {
	for _, id := range config.C().GetUsersID() {
		if id == chatID {
			return database.GetUserByChatID(ctx, chatID)
		}
	}
	return nil, fmt.Errorf("user is not configured")
}

func (m ManagementService) client(ctx context.Context) (*ext.Context, error) {
	getter := m.Client
	if getter == nil {
		getter = userclient.GetCtx
	}
	client := getter()
	if client == nil || client.Err() != nil {
		return nil, fmt.Errorf("userbot unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return tgutil.ClientWithContext(ctx, client), nil
}

func (m ManagementService) prepare(ctx context.Context, req RouteRequest, copyOnly bool) (*database.User, database.WatchChat, error) {
	u, err := ManagementUser(ctx, req.UserChatID)
	if err != nil {
		return nil, database.WatchChat{}, err
	}
	if req.Source == "" || strings.ContainsAny(req.Source+req.Target, " \t\r\n") || len(req.Source) > 256 || len(req.Target) > 256 || len(req.Filter) > 4096 {
		return nil, database.WatchChat{}, fmt.Errorf("invalid route input")
	}
	filter, err := validateAndNormalizeFilter(req.Filter)
	if err != nil {
		return nil, database.WatchChat{}, err
	}
	if req.Target == "" {
		req.Target = "0"
	}
	pt, err := parseTargetWithTopic(req.Target)
	if err != nil || pt.ChatIDArg == "0" && pt.TopicID != 0 {
		return nil, database.WatchChat{}, errWatchTargetTopicInvalid
	}
	client, err := m.client(ctx)
	if err != nil {
		return nil, database.WatchChat{}, err
	}
	source, err := tgutil.ParseChatID(client, req.Source)
	if err != nil || source == 0 {
		return nil, database.WatchChat{}, fmt.Errorf("invalid source: %v", err)
	}
	target, err := tgutil.ParseChatID(client, pt.ChatIDArg)
	if err != nil {
		return nil, database.WatchChat{}, err
	}
	if source == target {
		return nil, database.WatchChat{}, fmt.Errorf("source and target must differ")
	}
	if copyOnly && target == 0 {
		return nil, database.WatchChat{}, errCopyTargetLocalForbidden
	}
	if target == 0 && u.DefaultStorage == "" {
		return nil, database.WatchChat{}, fmt.Errorf("default storage is required")
	}
	sourceName, err := resolveWatchChatTitle(client, source)
	if err != nil {
		return nil, database.WatchChat{}, err
	}
	route := database.WatchChat{UserID: u.ID, ChatID: source, SourceName: sourceName,
		TargetID: target, TargetTopicID: pt.TopicID, Filter: filter}
	if target != 0 {
		route.TargetName, err = resolveWatchChatTitle(client, target)
		if err == nil && pt.TopicID > 0 {
			route.TargetTopicName, err = resolveForumTopicByID(client, target, pt.TopicID)
		}
	}
	return u, route, err
}

// CreateWatch persists only a new route; editing replaces a route explicitly.
func (m ManagementService) CreateWatch(ctx context.Context, req RouteRequest) (database.WatchChat, error) {
	u, route, err := m.prepare(ctx, req, false)
	if err != nil {
		return route, err
	}
	if route.TargetID != 0 {
		if err := m.ManagedTarget(ctx, req.UserChatID, strconv.FormatInt(route.TargetID, 10)); err != nil {
			return route, err
		}
	}
	watching, err := u.WatchingRoute(ctx, route.ChatID, route.TargetID, route.TargetTopicID)
	if err != nil {
		return route, err
	}
	if watching {
		return route, ErrManagementConflict
	}
	if err := u.WatchChat(ctx, route); err != nil {
		return route, err
	}
	return u.WatchChats[len(u.WatchChats)-1], nil
}

// StopWatch uses exactly the /unwatch admission and waiting-buffer boundary.
func (m ManagementService) StopWatch(ctx context.Context, chatID int64, id uint) error {
	u, err := ManagementUser(ctx, chatID)
	if err != nil {
		return err
	}
	return stopWatchRoute(ctx, u, id)
}

// CreateCopy submits one cancellable task without attaching request lifetime
// or creating Telegram progress messages. Counts appear in shared task history.
func (m ManagementService) CreateCopy(ctx, serviceCtx context.Context, req RouteRequest) (string, error) {
	if req.Count < 1 || req.Count > maxCopyCount {
		return "", errCopyCountTooLarge
	}
	_, route, err := m.prepare(ctx, req, true)
	if err != nil {
		return "", err
	}
	if err := m.ManagedTarget(ctx, req.UserChatID, strconv.FormatInt(route.TargetID, 10)); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := xid.New().String()
	if !copyfwd.TryBegin(req.UserChatID, id) {
		return "", ErrManagementConflict
	}
	task := copyfwd.NewTask(id, req.UserChatID, route.ChatID, route.TargetID, route.TargetTopicID, route.Filter, req.Count, nil)
	if err := core.AddTask(taskhistory.WithSource(serviceCtx, "admin"), task); err != nil {
		task.Discard()
		return "", err
	}
	return id, nil
}

// fetchTopics lists a bounded number of selectable, stable forum topic IDs.
func (m ManagementService) fetchTopics(ctx context.Context, chatID int64, target string) ([]TopicChoice, error) {
	if _, err := ManagementUser(ctx, chatID); err != nil {
		return nil, err
	}
	client, err := m.client(ctx)
	if err != nil {
		return nil, err
	}
	id, err := tgutil.ParseChatID(client, target)
	if err != nil || id == 0 {
		return nil, fmt.Errorf("invalid target")
	}
	peer, err := requireForumInputPeer(client, id)
	if err != nil {
		return nil, err
	}
	entries := make([]TopicChoice, 0)
	seen := make(map[int]bool)
	err = forEachForumTopicsPage(client, peer, func(topics []tg.ForumTopicClass, _ map[int]int) (bool, error) {
		for _, item := range topics {
			if topic, ok := item.(*tg.ForumTopic); ok && !seen[topic.ID] {
				seen[topic.ID] = true
				entries = append(entries, TopicChoice{ID: topic.ID, Title: topic.Title})
			}
		}
		if len(entries) > 1000 {
			return true, fmt.Errorf("topic list exceeds 1000 entries")
		}
		return false, nil
	})
	return entries, err
}

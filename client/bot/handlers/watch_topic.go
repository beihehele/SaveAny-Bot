package handlers

import (
	"errors"
	"fmt"
	"sort"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
)

var (
	errNotForumGroup = errors.New("not a forum group")
	errTopicNotFound = errors.New("forum topic not found")
)

type forumTopicEntry struct {
	title    string
	topMsgID int
}

func getChannelFromInputPeer(ctx *ext.Context, inputPeer tg.InputPeerClass) (*tg.Channel, error) {
	switch p := inputPeer.(type) {
	case *tg.InputPeerChannel:
		result, err := ctx.Raw.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
		})
		if err != nil {
			return nil, err
		}
		for _, ch := range result.GetChats() {
			if c, ok := ch.(*tg.Channel); ok && c.ID == p.ChannelID {
				return c, nil
			}
		}
		return nil, fmt.Errorf("channel not found")
	default:
		return nil, fmt.Errorf("not a supergroup or channel")
	}
}

func requireForumInputPeer(ctx *ext.Context, chatID int64) (tg.InputPeerClass, error) {
	inputPeer, err := ctx.ResolveInputPeerById(chatID)
	if err != nil {
		return nil, err
	}
	channel, err := getChannelFromInputPeer(ctx, inputPeer)
	if err != nil {
		return nil, err
	}
	if !channel.GetForum() {
		return nil, errNotForumGroup
	}
	return inputPeer, nil
}

func messageDatesFromResult(messages []tg.MessageClass) map[int]int {
	msgDateByID := make(map[int]int, len(messages))
	for _, m := range messages {
		if msg, ok := m.(*tg.Message); ok {
			msgDateByID[msg.ID] = msg.Date
		}
	}
	return msgDateByID
}

func nextForumTopicsOffset(topics []tg.ForumTopicClass, msgDateByID map[int]int) (offsetDate, offsetID, offsetTopic int, ok bool) {
	for i := len(topics) - 1; i >= 0; i-- {
		topic, isTopic := topics[i].(*tg.ForumTopic)
		if !isTopic {
			continue
		}
		offsetTopic = topic.ID
		offsetID = topic.TopMessage
		offsetDate = msgDateByID[topic.TopMessage]
		if offsetDate == 0 {
			offsetDate = topic.Date
		}
		return offsetDate, offsetID, offsetTopic, true
	}
	for i := len(topics) - 1; i >= 0; i-- {
		deleted, isDeleted := topics[i].(*tg.ForumTopicDeleted)
		if !isDeleted {
			continue
		}
		return 0, 0, deleted.ID, true
	}
	return 0, 0, 0, false
}

func forEachForumTopicsPage(ctx *ext.Context, inputPeer tg.InputPeerClass, fn func(topics []tg.ForumTopicClass, msgDateByID map[int]int) (stop bool, err error)) error {
	const limit = 100
	var offsetDate, offsetID, offsetTopic int

	for {
		result, err := ctx.Raw.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
			Peer:        inputPeer,
			Limit:       limit,
			OffsetDate:  offsetDate,
			OffsetID:    offsetID,
			OffsetTopic: offsetTopic,
		})
		if err != nil {
			return err
		}
		if len(result.Topics) == 0 {
			return nil
		}

		msgDateByID := messageDatesFromResult(result.Messages)
		stop, err := fn(result.Topics, msgDateByID)
		if err != nil || stop {
			return err
		}
		if len(result.Topics) < limit {
			return nil
		}

		var ok bool
		offsetDate, offsetID, offsetTopic, ok = nextForumTopicsOffset(result.Topics, msgDateByID)
		if !ok {
			return fmt.Errorf("forum topics pagination stalled")
		}
	}
}

func collectForumTopics(ctx *ext.Context, chatID int64) ([]forumTopicEntry, error) {
	inputPeer, err := requireForumInputPeer(ctx, chatID)
	if err != nil {
		return nil, err
	}

	var entries []forumTopicEntry
	err = forEachForumTopicsPage(ctx, inputPeer, func(topics []tg.ForumTopicClass, _ map[int]int) (bool, error) {
		for _, t := range topics {
			topic, ok := t.(*tg.ForumTopic)
			if !ok {
				continue
			}
			entries = append(entries, forumTopicEntry{
				title:    topic.Title,
				topMsgID: topic.TopMessage,
			})
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].title == entries[j].title {
			return entries[i].topMsgID < entries[j].topMsgID
		}
		return entries[i].title < entries[j].title
	})
	return entries, nil
}

func resolveForumTopicByTopMsgID(ctx *ext.Context, chatID int64, topMsgID int) (string, error) {
	if topMsgID <= 0 {
		return "", nil
	}
	inputPeer, err := requireForumInputPeer(ctx, chatID)
	if err != nil {
		return "", err
	}

	var found string
	err = forEachForumTopicsPage(ctx, inputPeer, func(topics []tg.ForumTopicClass, _ map[int]int) (bool, error) {
		for _, t := range topics {
			topic, ok := t.(*tg.ForumTopic)
			if !ok {
				continue
			}
			if topic.TopMessage == topMsgID {
				found = topic.Title
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errTopicNotFound
	}
	return found, nil
}

package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/pkg/inputresult"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

// MessageContext 保存消息和获取它所用的 context
type MessageContext struct {
	Message *tg.Message
	Client  *ext.Context
}

// getClientContext 获取可用的客户端上下文
// 优先使用 Bot，失败后回退到 Userbot
func getClientContext() (*ext.Context, error) {
	// 首先尝试获取 Bot context
	if botCtx := bot.ExtContext(); botCtx != nil {
		return botCtx, nil
	}

	// 回退到 Userbot
	if uc := userclient.GetCtx(); uc != nil {
		return uc, nil
	}

	return nil, fmt.Errorf("no client available (bot and userbot are not initialized)")
}

// resolveChatID 解析聊天 ID
func resolveChatID(ctx context.Context, idOrUsername string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	// 如果是数字 ID
	if id, err := strconv.ParseInt(idOrUsername, 10, 64); err == nil {
		// 私有频道 ID 需要加上 -100 前缀
		if id > 0 {
			return -1000000000000 - id, nil
		}
		return id, nil
	}

	// 获取可用的客户端上下文
	clientCtx, err := getClientContext()
	if err != nil {
		return 0, err
	}

	// 使用 tgutil 的 ParseChatID
	return tgutil.ParseChatID(tgutil.ClientWithContext(ctx, clientCtx), idOrUsername)
}

// ParseMessageLink 解析 Telegram 消息链接
// 支持格式:
// - https://t.me/username/123
// - https://t.me/c/123456789/123
// - https://t.me/c/123456789/111/456 (topic id)
// - https://t.me/username/123?comment=2 (评论)
func ParseMessageLink(ctx context.Context, link string) (int64, int, error) {
	u, err := url.Parse(link)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid URL: %w", err)
	}
	paths := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")

	if cmt := u.Query().Get("comment"); cmt != "" {
		// 频道评论的消息链接
		if len(paths) < 1 {
			return 0, 0, fmt.Errorf("invalid message link format: %s", link)
		}
		// 简化处理：返回错误，提示不支持评论链接
		return 0, 0, fmt.Errorf("comment links are not supported")
	}

	switch len(paths) {
	case 2: // https://t.me/username/123
		chatID, err := resolveChatID(ctx, paths[0])
		if err != nil {
			return 0, 0, fmt.Errorf("failed to resolve chat ID: %w", err)
		}
		msgID, err := strconv.Atoi(paths[1])
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse message ID: %w", err)
		}
		return chatID, msgID, nil
	case 3:
		// https://t.me/c/123456789/123
		// https://t.me/username/123/456 , 123: topic id
		chatPart, msgPart := paths[1], paths[2]
		if paths[0] != "c" {
			chatPart = paths[0]
		}
		chatID, err := resolveChatID(ctx, chatPart)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to resolve chat ID: %w", err)
		}
		msgID, err := strconv.Atoi(msgPart)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse message ID: %w", err)
		}
		return chatID, msgID, nil
	case 4:
		// https://t.me/c/123456789/111/456 111: topic id
		if paths[0] != "c" {
			return 0, 0, fmt.Errorf("invalid message link format: %s", link)
		}
		chatID, err := resolveChatID(ctx, paths[1])
		if err != nil {
			return 0, 0, fmt.Errorf("failed to resolve chat ID: %w", err)
		}
		msgID, err := strconv.Atoi(paths[3])
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse message ID: %w", err)
		}
		return chatID, msgID, nil
	}
	return 0, 0, fmt.Errorf("invalid message link format: %s", link)
}

// getMessageWithContext 通过 ID 获取消息，返回消息和使用的 context
// 确保消息获取和后续文件创建使用同一个 context
func getMessageWithContext(ctx context.Context, chatID int64, msgID int) (*MessageContext, error) {
	return getMessageFromClients(ctx, chatID, msgID, bot.ExtContext(), userclient.GetCtx())
}

func getMessageFromClients(ctx context.Context, chatID int64, msgID int, botCtx, uc *ext.Context) (*MessageContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 首先尝试使用 Bot
	if botCtx != nil {
		msg, err := tgutil.GetMessageByID(tgutil.ClientWithContext(ctx, botCtx), chatID, msgID)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			return &MessageContext{Message: msg, Client: botCtx}, nil
		}
	}

	// 回退到 Userbot
	if uc == nil {
		return nil, fmt.Errorf("userbot not initialized and bot cannot access this message")
	}

	msg, err := tgutil.GetMessageByID(tgutil.ClientWithContext(ctx, uc), chatID, msgID)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}

	return &MessageContext{Message: msg, Client: uc}, nil
}

// getGroupedMessagesWithContext retains the original client and reports only known
// original-message fallback; a successful query does not prove album completeness.
func getGroupedMessagesWithContext(ctx context.Context, source *MessageContext, chatID int64) ([]*tg.Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	msg := source.Message
	clientCtx := tgutil.ClientWithContext(ctx, source.Client)

	groupID, ok := msg.GetGroupedID()
	if !ok || groupID == 0 {
		return []*tg.Message{msg}, false, nil
	}

	// 使用获取原始消息的同一个 client 获取媒体组
	msgs, err := tgutil.GetGroupedMessages(clientCtx, chatID, msg)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if err != nil || len(msgs) == 0 {
		// Preserve the original-message fallback without implying album completeness.
		reason := "empty_result"
		if err != nil {
			reason = "query_error"
		}
		log.FromContext(ctx).Warn("Media group lookup failed; selecting only the original message",
			"source_chat_id", chatID, "message_id", msg.GetID(), "group_id", groupID,
			"reason", reason, "error", err)
		return []*tg.Message{msg}, true, nil
	}

	return msgs, false, nil
}

// ExtractFilesFromLinks 从消息链接中提取文件
// 每个文件的处理流程：解析链接 -> 获取消息 -> 获取媒体组 -> 创建文件对象
// 对于单个文件，全程使用同一个 client context，不会交叉
func ExtractFilesFromLinks(ctx context.Context, links []string) ([]tfile.TGFileMessage, error) {
	return extractFilesFromLinks(ctx, links, ParseMessageLink, getMessageWithContext)
}

func extractFilesFromLinks(ctx context.Context, links []string,
	parseLink func(context.Context, string) (int64, int, error),
	getMessage func(context.Context, int64, int) (*MessageContext, error),
) ([]tfile.TGFileMessage, error) {
	files, _, err := extractFilesFromLinksReport(ctx, links, parseLink, getMessage)
	return files, err
}

func extractFilesFromLinksReport(ctx context.Context, links []string,
	parseLink func(context.Context, string) (int64, int, error),
	getMessage func(context.Context, int64, int) (*MessageContext, error),
) ([]tfile.TGFileMessage, inputresult.Report, error) {
	var report inputresult.Report
	logger := log.FromContext(ctx)
	var files tfile.LinkedFiles
	for index, link := range links {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		link = strings.TrimSpace(link)
		if !isValidMessageLink(link) {
			report.Add(index+1, "invalid_link", 0)
			continue
		}
		chatID, msgID, err := parseLink(ctx, link)
		if ctx.Err() != nil {
			return nil, report, ctx.Err()
		}
		if err != nil {
			logger.Errorf("Failed to parse message link %s: %v", link, err)
			report.Add(index+1, "resolution_failed", 0)
			continue
		}
		u, _ := url.Parse(link)
		single := u != nil && u.Query().Has("single")
		msgCtx, err := getMessage(ctx, chatID, msgID)
		if ctx.Err() != nil {
			return nil, report, ctx.Err()
		}
		if err != nil || msgCtx == nil || msgCtx.Message == nil || msgCtx.Client == nil {
			logger.Errorf("Failed to get message %d from chat %d: %v", msgID, chatID, err)
			report.Add(index+1, "message_unavailable", 0)
			continue
		}
		msg, clientCtx := msgCtx.Message, msgCtx.Client
		if msg.Media == nil {
			report.Add(index+1, "no_media", 0)
			continue
		}
		before := len(files.Files())
		code := "ready"
		add := func(message *tg.Message) {
			if message == nil || message.Media == nil {
				code = "no_media"
				return
			}
			if err := files.Add(chatID, clientCtx.Raw, message); err != nil {
				logger.Errorf("Failed to create file from media: %v", err)
				code = "unsupported_media"
			}
		}
		groupID, isGroup := msg.GetGroupedID()
		if isGroup && groupID != 0 && !single {
			groupMsgs, albumFallback, err := getGroupedMessagesWithContext(ctx, msgCtx, chatID)
			if ctx.Err() != nil {
				return nil, report, ctx.Err()
			}
			if err != nil {
				return nil, report, err
			}
			for _, gmsg := range groupMsgs {
				add(gmsg)
			}
			if albumFallback {
				report.AddWithAlbumFallback(index+1, code, len(files.Files())-before)
			} else {
				report.Add(index+1, code, len(files.Files())-before)
			}
			continue
		}
		add(msg)
		report.Add(index+1, code, len(files.Files())-before)
	}
	if err := ctx.Err(); err != nil {
		return nil, report, err
	}
	if len(files.Files()) == 0 {
		return nil, report, &inputresult.Error{Report: report, Cause: fmt.Errorf("no files found in provided links")}
	}
	return files.Files(), report, nil
}

// isValidMessageLink 检查是否是有效的 Telegram 消息链接
func isValidMessageLink(link string) bool {
	for _, prefix := range []string{
		"https://t.me/",
		"http://t.me/",
		"https://telegram.me/",
		"http://telegram.me/",
	} {
		if strings.HasPrefix(link, prefix) {
			return true
		}
	}
	return false
}

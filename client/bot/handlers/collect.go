package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"github.com/rs/xid"

	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/re"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/batchtfile"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/krau/SaveAny-Bot/storage/telegram"
)

type collectArgs struct {
	URL    string
	Target int64
	Topic  int
}

func parseCollectArgs(args []string) (collectArgs, error) {
	var result collectArgs
	if len(args) < 1 || len(args) > 3 || re.TgMessageLinkRegexp.FindString(args[0]) != args[0] {
		return result, fmt.Errorf("invalid collect arguments")
	}
	result.URL = args[0]
	if len(args) > 1 {
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id >= 0 {
			return result, fmt.Errorf("invalid collection target")
		}
		result.Target = id
	}
	if len(args) > 2 {
		topic, err := strconv.ParseInt(args[2], 10, 32)
		if err != nil || topic < 0 {
			return result, fmt.Errorf("invalid collection topic")
		}
		result.Topic = int(topic)
	}
	return result, nil
}

func handleCollectCmd(ctx *ext.Context, update *ext.Update) error {
	args, err := parseCollectArgs(strings.Fields(update.EffectiveMessage.Text)[1:])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCollectHelpText)), nil)
		return dispatcher.EndGroups
	}
	var store *telegram.Store
	for _, candidate := range storage.GetUserStorages(ctx, update.GetUserChat().GetID()) {
		if collection, ok := candidate.(*telegram.Store); ok {
			if store != nil {
				ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCollectErrorStorageAmbiguous)), nil)
				return dispatcher.EndGroups
			}
			store = collection
		}
	}
	if store == nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCollectErrorStorageRequired)), nil)
		return dispatcher.EndGroups
	}
	if args.Target != 0 {
		store, err = store.WithTarget(args.Target, args.Topic)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCollectHelpText)), nil)
			return dispatcher.EndGroups
		}
	}
	// The existing extractor handles albums, source access and duplicate links.
	replied, files, edit, err := shortcut.GetFilesFromLinksWithReplyEdit(ctx, update, []string{args.URL})
	if err != nil {
		return err
	}
	elements := make([]batchtfile.TaskElement, 0, len(files))
	for _, file := range files {
		elem, err := batchtfile.NewTaskElement(store, "", file)
		if err != nil {
			edit(i18n.T(i18nk.BotMsgCommonErrorTaskCreateFailed, map[string]any{"Error": err.Error()}), nil)
			return dispatcher.EndGroups
		}
		elements = append(elements, *elem)
	}
	task := batchtfile.NewBatchTGFileTask(xid.New().String(), elements, batchtfile.NewProgressTracker(replied.ID, update.GetUserChat().GetID()))
	edit(task.Title()+"\n"+i18n.T(i18nk.BotMsgTasksStatusQueued), &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{tgutil.BuildCancelButton(task.TaskID())}}}})
	if err := core.AddTask(tgutil.ExtWithContext(ctx.Context, ctx), task); err != nil {
		edit(i18n.T(i18nk.BotMsgCommonErrorTaskAddFailed, map[string]any{"Error": err.Error()}), nil)
		return dispatcher.EndGroups
	}
	return dispatcher.EndGroups
}

package handlers

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/blang/semver"
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/updater"
)

func handleUpdateCmd(ctx *ext.Context, u *ext.Update) error {
	currentV, err := semver.Parse(config.Version)
	if err != nil {
		ctx.Reply(u, ext.ReplyTextString(i18n.T(i18nk.BotMsgUpdateErrorVersionVarInvalid, map[string]any{
			"Error": err.Error(),
		})), nil)
		return dispatcher.EndGroups
	}
	latest, ok, err := updater.DetectLatest(ctx, config.GitRepo)
	if err != nil {
		ctx.Reply(u, ext.ReplyTextString(i18n.T(i18nk.BotMsgUpdateErrorCheckLatestFailed, map[string]any{
			"Error": err.Error(),
		})), nil)
		return dispatcher.EndGroups
	}
	if !ok {
		ctx.Reply(u, ext.ReplyTextString(i18n.T(i18nk.BotMsgUpdateErrorNoReleaseFound, nil)), nil)
		return dispatcher.EndGroups
	}
	if latest.Version().Major != currentV.Major {
		ctx.Reply(u, ext.ReplyTextString(i18n.T(i18nk.BotMsgUpdateInfoMajorUpgradeRequired, map[string]any{
			"Current": currentV.String(),
			"Latest":  latest.Version().String(),
		})), nil)
		return dispatcher.EndGroups
	}
	if errors.Is(updater.CheckUpgrade(currentV, latest), updater.ErrAlreadyLatest) {
		ctx.Reply(u, ext.ReplyTextString(i18n.T(i18nk.BotMsgUpdateInfoAlreadyLatest, map[string]any{
			"Version": config.Version,
		})), nil)
		return dispatcher.EndGroups
	}
	indocker := config.Docker == "true"
	if !indocker && (updater.CheckEnvironment(false) != nil || latest.VerificationError() != nil) {
		ctx.Reply(u, ext.ReplyTextString(i18n.T(i18nk.BotMsgUpdateInfoManualUpgradeRequired, nil)), nil)
		return dispatcher.EndGroups
	}
	ctx.Sender.To(u.GetUserChat().AsInputPeer()).StyledText(ctx, html.String(nil, func() string {
		md := latest.ReleaseNotes()
		md = regexp.MustCompile(`(?m)^###\s+&nbsp;&nbsp;&nbsp;(.+)$`).ReplaceAllString(md, "<b>$1</b>")
		md = regexp.MustCompile(`(?m)^#####\s+&nbsp;&nbsp;&nbsp;&nbsp;(.+)$`).ReplaceAllString(md, "<i>$1</i>")

		md = regexp.MustCompile(`(?m)^- `).ReplaceAllString(md, "• ")

		md = regexp.MustCompile(`\[\((\w{6,})\)\]\((https?://[^\s)]+)\)`).ReplaceAllString(md, `(<a href="$2">$1</a>)`)

		md = regexp.MustCompile(`\[(.+?)\]\((https?://[^\s)]+)\)`).ReplaceAllString(md, `<a href="$2">$1</a>`)

		md = strings.ReplaceAll(md, "&nbsp;", " ")

		return `<blockquote expandable>` + md + `</blockquote>`
	}()))
	if indocker {
		text := i18n.T(i18nk.BotMsgUpdateInfoNewVersionInDocker, map[string]any{
			"Latest":      latest.Version().String(),
			"Current":     config.Version,
			"PublishedAt": latest.PublishedAt().Format("2006-01-02 15:04:05"),
		})
		ctx.Reply(u, ext.ReplyTextString(text), nil)
		return dispatcher.EndGroups
	}
	text := i18n.T(i18nk.BotMsgUpdateInfoNewVersionPromptUpgrade, map[string]any{
		"Latest":      latest.Version().String(),
		"Current":     config.Version,
		"SizeMB":      float64(latest.AssetByteSize()) / (1024 * 1024),
		"URL":         latest.AssetURL(),
		"PublishedAt": latest.PublishedAt().Format("2006-01-02 15:04:05"),
	})
	ctx.Reply(u, ext.ReplyTextString(text), &ext.ReplyOpts{
		Markup: &tg.ReplyInlineMarkup{
			Rows: []tg.KeyboardButtonRow{
				{
					Buttons: []tg.KeyboardButtonClass{
						&tg.KeyboardButtonCallback{
							Text: i18n.T(i18nk.BotMsgUpdateButtonUpgrade, nil),
							Data: []byte("update:" + strconv.FormatInt(latest.AssetID(), 10)),
						},
					},
				},
			},
		},
	})
	return dispatcher.EndGroups
}

func handleUpdateCallback(ctx *ext.Context, u *ext.Update) error {
	if u == nil || u.CallbackQuery == nil {
		return dispatcher.EndGroups
	}
	edit := func(key i18nk.Key, values map[string]any) error {
		_, err := ctx.EditMessage(u.GetUserChat().GetID(), &tg.MessagesEditMessageRequest{
			ID: u.CallbackQuery.GetMsgID(), Message: i18n.T(key, values),
		})
		if err != nil {
			return err
		}
		return dispatcher.EndGroups
	}
	assetID, err := parseUpdateAssetID(u.CallbackQuery.Data)
	if err != nil {
		return edit(i18nk.BotMsgUpdateErrorReleaseChanged, nil)
	}
	currentV, err := semver.Parse(config.Version)
	if err != nil {
		return edit(i18nk.BotMsgUpdateErrorVersionVarInvalid, map[string]any{"Error": err.Error()})
	}
	if err := updater.CheckEnvironment(config.Docker == "true"); err != nil {
		return edit(i18nk.BotMsgUpdateInfoManualUpgradeRequired, nil)
	}
	latest, found, err := updater.DetectLatest(ctx, config.GitRepo)
	if err != nil {
		return edit(i18nk.BotMsgUpdateErrorCheckLatestFailed, map[string]any{"Error": err.Error()})
	}
	if !found {
		latest = nil
	}
	if err := updater.CheckApprovedUpgrade(currentV, latest, assetID); err != nil {
		if errors.Is(err, updater.ErrReleaseChanged) {
			return edit(i18nk.BotMsgUpdateErrorReleaseChanged, nil)
		}
		if errors.Is(err, updater.ErrMajorUpgrade) {
			return edit(i18nk.BotMsgUpdateInfoMajorUpgradeRequired, map[string]any{"Current": currentV.String(), "Latest": latest.Version().String()})
		}
		return edit(i18nk.BotMsgUpdateInfoAlreadyLatest, map[string]any{"Version": config.Version})
	}
	if err := latest.VerificationError(); err != nil {
		return edit(i18nk.BotMsgUpdateInfoManualUpgradeRequired, nil)
	}
	if err := edit(i18nk.BotMsgUpdateInfoUpgradingWithVersion, map[string]any{"Current": config.Version}); !errors.Is(err, dispatcher.EndGroups) {
		return err
	}
	backup, err := updater.Apply(ctx, currentV, latest)
	if err != nil {
		return edit(i18nk.BotMsgUpdateErrorUpgradeFailed, map[string]any{"Error": err.Error()})
	}
	if err := edit(i18nk.BotMsgUpdateInfoUpgradeSuccess, map[string]any{"Version": latest.Version().String(), "Backup": backup}); !errors.Is(err, dispatcher.EndGroups) {
		log.FromContext(ctx).Errorf("Updated executable but failed to report success: %v", err)
	}
	return errors.New("SAVEANTBOT-RESTART")
}

func parseUpdateAssetID(data []byte) (int64, error) {
	if len(data) > 64 {
		return 0, errors.New("invalid update callback")
	}
	value, ok := strings.CutPrefix(string(data), "update:")
	if !ok {
		return 0, errors.New("expired update callback")
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, errors.New("invalid update asset ID")
	}
	return id, nil
}

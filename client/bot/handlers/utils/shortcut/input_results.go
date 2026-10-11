package shortcut

import (
	"strings"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/pkg/inputresult"
)

// inputWarnings limits Telegram feedback while preserving the complete selection.
func inputWarnings(report inputresult.Report) string {
	reasons := map[string]i18nk.Key{
		"resolution_failed":   i18nk.BotMsgCommonLinkResultResolutionFailed,
		"message_unavailable": i18nk.BotMsgCommonLinkResultMessageUnavailable,
		"no_media":            i18nk.BotMsgCommonLinkResultNoMedia,
		"unsupported_media":   i18nk.BotMsgCommonLinkResultUnsupportedMedia,
		"album_unavailable":   i18nk.BotMsgCommonLinkResultAlbumUnavailable,
		"duplicate":           i18nk.BotMsgCommonLinkResultDuplicate,
	}
	var lines []string
	omitted := report.InputResultsOmitted
	for _, result := range report.InputResults {
		if result.Status == "ready" {
			continue
		}
		if len(lines) >= 10 {
			omitted++
			continue
		}
		key, ok := reasons[result.Code]
		if !ok {
			key = i18nk.BotMsgCommonLinkResultUnsupportedMedia
		}
		lines = append(lines, i18n.T(i18nk.BotMsgCommonInputResultLine, map[string]any{
			"Index": result.Index, "Reason": i18n.T(key, nil), "NewFiles": result.NewFiles,
		}))
	}
	if omitted > 0 {
		lines = append(lines, i18n.T(i18nk.BotMsgCommonInputResultOmitted, map[string]any{"Count": omitted}))
	}
	if len(lines) == 0 {
		return ""
	}
	return i18n.T(i18nk.BotMsgCommonInputResultHeader, nil) + "\n" + strings.Join(lines, "\n")
}

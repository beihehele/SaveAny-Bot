package tfile

import "github.com/krau/SaveAny-Bot/common/utils/progressutil"

func shouldUpdateProgress(total, downloaded int64, lastUpdatePercent int) bool {
	return progressutil.ShouldUpdate(total, downloaded, lastUpdatePercent)
}

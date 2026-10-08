package tdler

import (
	"context"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/utils/dlutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/consts/tglimit"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func NewDownloader(file tfile.TGFile) *downloader.Builder {
	return downloader.NewDownloader().WithPartSize(tglimit.MaxPartSize).
		Download(eofAwareClient{Client: file.Dler(), size: file.Size()}, file.Location()).
		WithThreads(dlutil.BestThreads(file.Size(), config.C().Threads))
}

// eofAwareClient avoids OFFSET_INVALID for a final request at a known EOF.
// A zero size means unknown (for example photos), so it must still be fetched.
type eofAwareClient struct {
	downloader.Client
	size int64
}

func (c eofAwareClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	if c.size > 0 && req.Offset >= c.size {
		return &tg.UploadFile{}, nil
	}
	return c.Client.UploadGetFile(ctx, req)
}

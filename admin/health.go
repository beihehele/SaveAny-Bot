package admin

import (
	"context"
	"errors"
	"sort"

	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
)

type uploadOutbox struct {
	Storage         string `json:"storage"`
	PendingFiles    int64  `json:"pending_files"`
	FailedFiles     int64  `json:"failed_files"`
	ReservedBytes   int64  `json:"reserved_bytes"`
	MaxPendingBytes int64  `json:"max_pending_bytes"`
}

// uploadOutboxes uses the same reservation accounting as download admission.
// It includes incomplete downloads and retained files, without scanning payloads.
func uploadOutboxes(ctx context.Context) ([]uploadOutbox, error) {
	result := make([]uploadOutbox, 0)
	names := make([]string, 0)
	for _, cfg := range config.C().Storages {
		if cfg, ok := cfg.(*storcfg.ShardReelStorageConfig); ok {
			result = append(result, uploadOutbox{Storage: cfg.Name, MaxPendingBytes: cfg.MaxPendingBytes})
			names = append(names, cfg.Name)
		}
	}
	if len(result) == 0 {
		return result, nil
	}
	db := database.ShardReelDB(ctx)
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	var totals []uploadOutbox
	if err := db.Model(&database.ShardReelFile{}).Select(`storage,
		SUM(CASE WHEN status IN ('preparing','queued','uploading','processing','retry_wait','cancelling') THEN 1 ELSE 0 END) AS pending_files,
		SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed_files,
		SUM(CASE WHEN local_clean = false THEN size ELSE 0 END) AS reserved_bytes`).
		Where("storage IN ?", names).Group("storage").Scan(&totals).Error; err != nil {
		return nil, err
	}
	byName := make(map[string]uploadOutbox, len(totals))
	for _, total := range totals {
		byName[total.Storage] = total
	}
	for i := range result {
		total := byName[result[i].Storage]
		result[i].PendingFiles, result[i].FailedFiles, result[i].ReservedBytes = total.PendingFiles, total.FailedFiles, total.ReservedBytes
	}
	return result, nil
}

type diskObservation struct {
	Storage        string `json:"storage"`
	Available      bool   `json:"available"`
	AvailableBytes uint64 `json:"available_bytes"`
}

func localDiskObservations() []diskObservation {
	cfg := config.C()
	result := make([]diskObservation, 0)
	for name := range storage.AllStorages() {
		local, ok := cfg.GetStorageByName(name).(*storcfg.LocalStorageConfig)
		if !ok {
			continue
		}
		available, err := fsutil.AvailableDiskBytes(local.BasePath)
		result = append(result, diskObservation{Storage: name, Available: err == nil, AvailableBytes: available})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Storage < result[j].Storage })
	return result
}

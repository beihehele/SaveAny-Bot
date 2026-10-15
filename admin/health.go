package admin

import (
	"sort"

	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/storage"
)

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

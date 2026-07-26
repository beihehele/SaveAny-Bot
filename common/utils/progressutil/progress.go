package progressutil

var updateLevels = []struct {
	size        int64 // file size threshold
	stepPercent int   // update every N percent
}{
	{10 << 20, 100},
	{50 << 20, 20},
	{200 << 20, 10},
	{500 << 20, 5},
}

// ShouldUpdate reports whether progress UI should refresh for the given download state.
func ShouldUpdate(total, downloaded int64, lastUpdatePercent int) bool {
	if total <= 0 || downloaded <= 0 {
		return false
	}

	percent := int((downloaded * 100) / total)
	if percent <= lastUpdatePercent {
		return false
	}

	step := updateLevels[len(updateLevels)-1].stepPercent
	for _, lvl := range updateLevels {
		if total < lvl.size {
			step = lvl.stepPercent
			break
		}
	}

	return percent >= lastUpdatePercent+step
}

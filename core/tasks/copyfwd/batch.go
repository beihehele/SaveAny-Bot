package copyfwd

// packForwardBatches packs message IDs into batches of at most maxBatch,
// never splitting a same-grouped_id album or an ID-contiguous media run
// (album + caption-less continuations).
func packForwardBatches(ids []int, meta map[int]ScanMsg, maxBatch int) [][]int {
	if len(ids) == 0 {
		return nil
	}
	if maxBatch < 1 {
		maxBatch = forwardBatch
	}

	clusters := clusterForwardIDs(ids, meta)
	batches := make([][]int, 0, len(clusters))
	var cur []int
	flush := func() {
		if len(cur) == 0 {
			return
		}
		batches = append(batches, cur)
		cur = nil
	}

	for _, cluster := range clusters {
		if len(cluster) > maxBatch {
			// Oversized cluster (should be rare; albums ≪ 100): flush current, emit alone.
			flush()
			batches = append(batches, cluster)
			continue
		}
		if len(cur)+len(cluster) > maxBatch {
			flush()
		}
		cur = append(cur, cluster...)
	}
	flush()
	return batches
}

func clusterForwardIDs(ids []int, meta map[int]ScanMsg) [][]int {
	if len(ids) == 0 {
		return nil
	}
	var clusters [][]int
	cur := []int{ids[0]}
	for i := 1; i < len(ids); i++ {
		id := ids[i]
		last := cur[len(cur)-1]
		if sameForwardCluster(meta[last], meta[id], last, id) {
			cur = append(cur, id)
			continue
		}
		clusters = append(clusters, cur)
		cur = []int{id}
	}
	clusters = append(clusters, cur)
	return clusters
}

func sameForwardCluster(a, b ScanMsg, aID, bID int) bool {
	if a.HasGroup && b.HasGroup && a.GroupedID == b.GroupedID && a.GroupedID != 0 {
		return true
	}
	// Contiguous album / continuation run (IDs must be adjacent).
	if a.HasGroup && b.HasGroup && bID == aID+1 {
		return true
	}
	return false
}

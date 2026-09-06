package rag

import "sort"

func FuseRRF(lists []RankedList, limit int, k float64) []Result {
	if k <= 0 {
		k = 60
	}
	type aggregate struct {
		result   Result
		score    float64
		bestRank int
	}
	aggregates := map[string]*aggregate{}
	for _, list := range lists {
		for index, result := range list {
			rank := result.Rank
			if rank <= 0 {
				rank = index + 1
			}
			key := resultKey(result)
			if _, ok := aggregates[key]; !ok {
				copy := result
				aggregates[key] = &aggregate{result: copy, bestRank: rank}
			}
			aggregates[key].score += 1 / (k + float64(rank))
			if rank < aggregates[key].bestRank {
				aggregates[key].bestRank = rank
			}
		}
	}

	results := make([]Result, 0, len(aggregates))
	for _, aggregate := range aggregates {
		result := aggregate.result
		result.Score = aggregate.score
		result.Rank = aggregate.bestRank
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Rank != results[j].Rank {
			return results[i].Rank < results[j].Rank
		}
		return resultKey(results[i]) < resultKey(results[j])
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	for i := range results {
		results[i].Rank = i + 1
	}
	return results
}

func resultKey(result Result) string {
	if result.EvidenceID != "" {
		return result.EvidenceID
	}
	if result.Chunk.ID != "" {
		return result.Chunk.ID
	}
	return result.Document.ID
}

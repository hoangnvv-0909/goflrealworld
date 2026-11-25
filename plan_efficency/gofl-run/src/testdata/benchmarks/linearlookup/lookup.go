package linearlookup

// CountMatches uses a fresh linear scan for each query.
func CountMatches(values, queries []int) int {
	matches := 0
	for _, query := range queries {
		for _, value := range values {
			if value == query {
				matches++
				break
			}
		}
	}
	return matches
}

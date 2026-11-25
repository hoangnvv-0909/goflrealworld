package sortedinsert

// SortByInsertion maintains sorted order by shifting larger elements for each insertion.
func SortByInsertion(values []int) []int {
	sorted := make([]int, 0, len(values))
	for _, value := range values {
		sorted = append(sorted, value)
		position := len(sorted) - 1
		for position > 0 && sorted[position-1] > value {
			sorted[position] = sorted[position-1]
			position--
		}
		sorted[position] = value
	}
	return sorted
}

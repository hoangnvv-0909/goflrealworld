package duplicates

// Unique scans the growing output slice for each input value.
func Unique(values []int) []int {
	out := make([]int, 0, len(values))
	for _, value := range values {
		found := false
		for _, previous := range out {
			if value == previous {
				found = true
				break
			}
		}
		if !found {
			out = append(out, value)
		}
	}
	return out
}

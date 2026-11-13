package utils

// Must returns v, or panics if err is not nil.
// Use it only where an error means a programming mistake.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

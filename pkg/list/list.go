package list

func Contains[T comparable](sl []T, ele T) bool {
	for _, x := range sl {
		if x == ele {
			return true
		}
	}

	return false
}

package v1

func StringInArray(val string, array []string) bool {
	for i := range array {
		if array[i] == val {
			return true
		}
	}
	return false
}

func RemoveString(s string, slice []string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func UniqueAppend(inSlice []string, strings ...string) []string {
	for _, s := range strings {
		if !StringInArray(s, inSlice) {
			inSlice = append(inSlice, s)
		}
	}
	return inSlice
}

package utils

import (
	"reflect"
	"regexp"
)

// StringMapsMatch returns true if the provided maps contain the same keys and values, otherwise false
func StringMapsMatch(m1, m2 map[string]string, ignoreKeys []string) bool {
	// if both are empty then they must match
	if (m1 == nil || len(m1) == 0) && (m2 == nil || len(m2) == 0) {
		return true
	}

	ignoreMap := make(map[string]struct{})
	for _, k := range ignoreKeys {
		ignoreMap[k] = struct{}{}
	}

	for k, v := range m1 {
		if _, ignore := ignoreMap[k]; ignore {
			continue
		}
		v2, ok := m2[k]
		if !ok || v2 != v {
			return false
		}
	}
	for k, v := range m2 {
		if _, ignore := ignoreMap[k]; ignore {
			continue
		}
		v1, ok := m1[k]
		if !ok || v1 != v {
			return false
		}
	}
	return true
}

// ByteMapsMatch is like stringMapsMatch but for maps of byte arrays
func ByteMapsMatch(m1, m2 map[string][]byte) bool {
	if len(m1) != len(m2) {
		return false
	}
	for k, v := range m1 {
		v2, ok := m2[k]
		if !ok {
			return false
		}
		if len(v2) != len(v) {
			return false
		}
		for i, c := range v {
			if v2[i] != c {
				return false
			}
		}
	}
	return true
}

// SecretStringByteMatch returns true if map[string]string and map[string][]byte have the same contents
func SecretStringByteMatch(s map[string]string, b map[string][]byte) bool {
	/*
		The generated secret has always at least two values, username and password. Also, they may not be called
		`username` and `password` so we need to check for that.
		The DbSecret could be null, contain only username, only password, both or additional entries
	*/
	if s == nil && len(b) == 2 {
		return true
	}
	if s == nil && len(b) > 2 {
		return false
	}

	passwordKey := "password"
	usernameKey := "username"
	if s["password"] != "" {
		passwordKey = s["password"]
		if _, ok := b[passwordKey]; !ok {
			return false
		}
	}
	if s["password"] == "" && string(b["password"]) == "" {
		return false
	}

	if s["username"] != "" {
		usernameKey = s["username"]
		if _, ok := b[usernameKey]; !ok {
			return false
		}
	}
	if s["username"] == "" && string(b["username"]) == "" {
		return false
	}
	s1 := reflect.ValueOf(s).Interface().(map[string]string)
	b1 := reflect.ValueOf(b).Interface().(map[string][]byte)
	delete(s1, "username")
	delete(s1, "password")
	delete(b1, usernameKey)
	delete(b1, passwordKey)
	for key, value1 := range s1 {
		if value2, ok := b1[key]; !ok || string(value2) != value1 {
			return false
		}
	}

	return true
}

func MergeMap(dst map[string]string, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

// ContainsString checks if a slice contains a particular string
func ContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// RemoveString returns a copy of a slice with the specified string removed if it is found
func RemoveString(slice []string, s string) (result []string) {
	if len(slice) < 1 {
		return
	}
	result = make([]string, 0, len(slice)-1)
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return
}

// FindAllGroups returns a map with each match group. The map key corresponds to the match group name.
// A nil return value indicates no matches.
func FindAllGroups(re *regexp.Regexp, s string) map[string]string {
	matches := re.FindStringSubmatch(s)
	subnames := re.SubexpNames()
	if matches == nil || len(matches) != len(subnames) {
		return nil
	}

	matchMap := map[string]string{}
	for i := 1; i < len(matches); i++ {
		matchMap[subnames[i]] = matches[i]
	}
	return matchMap
}

func K8sSecretFound(m map[string]string) bool {
	for _, k := range []string{"namespace", "secretName", "key"} {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

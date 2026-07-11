package handler

import "strings"

func displayModelName(modelNames map[string]string, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if modelNames != nil {
		if name, ok := modelNames[raw]; ok && strings.TrimSpace(name) != "" {
			return name
		}
	}
	return raw
}

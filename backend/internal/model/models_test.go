package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestEventLogMediaURLsUseTextColumns(t *testing.T) {
	typeOfEvent := reflect.TypeOf(EventLog{})
	for _, fieldName := range []string{"File", "UpstreamResultURL"} {
		field, ok := typeOfEvent.FieldByName(fieldName)
		if !ok {
			t.Fatalf("missing EventLog.%s", fieldName)
		}
		tag := field.Tag.Get("gorm")
		if !strings.Contains(tag, "type:text") || strings.Contains(tag, "size:") {
			t.Fatalf("EventLog.%s gorm tag=%q", fieldName, tag)
		}
	}
}

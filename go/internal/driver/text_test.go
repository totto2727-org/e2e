package driver

import (
	"encoding/json"
	"testing"
)

func TestTextValidation(t *testing.T) {
	for _, input := range []string{`null`, `true`, `0`, `""`, `"a\u0000b"`} {
		if _, err := text(request{"path": json.RawMessage(input)}, "path"); err == nil {
			t.Fatalf("accepted invalid path %s", input)
		}
	}
	value, err := text(request{"path": json.RawMessage(`"/tmp/socket"`)}, "path")
	if err != nil || value != "/tmp/socket" {
		t.Fatalf("valid path: %q %v", value, err)
	}
}

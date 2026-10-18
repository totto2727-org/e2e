package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestByteArrays(t *testing.T) {
	for _, input := range []string{"null", "[null]", "[true]", "[-1]", "[256]", "[1.5]", "{}", "\"abc\""} {
		t.Run(input, func(t *testing.T) {
			if _, err := byteData(json.RawMessage(input)); err == nil {
				t.Fatalf("accepted invalid byte array %s", input)
			}
		})
	}
	for _, input := range []string{"[]", "[0,128,255]"} {
		data, err := byteData(json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(numbers(data))
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != input {
			t.Fatalf("byte round trip: got %s want %s", encoded, input)
		}
	}
}
func TestValidation(t *testing.T) {
	for _, input := range []string{"null", "[]", "1", "\"x\""} {
		if _, err := parse(input); err == nil {
			t.Errorf("accepted request %s", input)
		}
	}
	for _, input := range []string{"0", "-1", "60.1", "null", "true", "\"1\""} {
		if _, err := duration(request{"timeout": json.RawMessage(input)}); err == nil {
			t.Errorf("accepted timeout %s", input)
		}
	}
	for _, input := range []string{"0", "65536", "1.5", "null", "true"} {
		if _, _, err := geometry(request{"rows": json.RawMessage(input)}); err == nil {
			t.Errorf("accepted geometry %s", input)
		}
	}
}
func TestReplyWireShape(t *testing.T) {
	for _, test := range []struct {
		value reply
		want  string
	}{
		{success(nil), `{"ok":true,"value":null}`},
		{failure(errors.New("failure"), nil), `{"ok":false,"error":"failure","observation":null}`},
		{success(numbers(nil)), `{"ok":true,"value":[]}`},
	} {
		data, err := json.Marshal(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != test.want {
			t.Errorf("got %s want %s", data, test.want)
		}
	}
	var output bytes.Buffer
	if code := Run([]string{"snapshot", "null"}, &output); code != 1 {
		t.Errorf("helper failure exit %d", code)
	}
	if !strings.Contains(output.String(), `"ok":false`) {
		t.Fatalf("missing structured failure: %s", output.String())
	}
}

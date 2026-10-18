// Package driver implements the private Caseyard terminal transport.
package driver

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

type request map[string]json.RawMessage
type reply struct {
	OK          bool   `json:"ok"`
	Value       any    `json:"value,omitempty"`
	Error       string `json:"error,omitempty"`
	Observation any    `json:"observation,omitempty"`
}

func success(value any) reply { return reply{OK: true, Value: value} }
func failure(err error, observation any) reply {
	return reply{Error: err.Error(), Observation: observation}
}

// MarshalJSON keeps null fields required by the wire protocol.
func (r reply) MarshalJSON() ([]byte, error) {
	if r.OK {
		return json.Marshal(struct {
			OK    bool `json:"ok"`
			Value any  `json:"value"`
		}{true, r.Value})
	}
	return json.Marshal(struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Observation any    `json:"observation"`
	}{false, r.Error, r.Observation})
}
func parse(data string) (request, error) {
	var r request
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("request must be an object")
	}
	return r, nil
}
func text(r request, key string) (string, error) {
	var v string
	if err := json.Unmarshal(r[key], &v); err != nil || v == "" || strings.ContainsRune(v, 0) {
		return "", fmt.Errorf("%s must be a nonempty NUL-free string", key)
	}
	return v, nil
}
func byteData(raw json.RawMessage) ([]byte, error) {
	var values []json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("expected an array of bytes")
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("expected an array of bytes: %w", err)
	}
	data := make([]byte, len(values))
	for i, raw := range values {
		var v int
		if string(raw) == "null" {
			return nil, fmt.Errorf("expected an array of bytes")
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("expected an array of bytes: %w", err)
		}
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("expected an array of bytes")
		}
		data[i] = byte(v)
	}
	return data, nil
}
func numbers(data []byte) []int {
	result := make([]int, len(data))
	for i, v := range data {
		result[i] = int(v)
	}
	return result
}
func duration(r request) (time.Duration, error) {
	v := 50.0
	if raw, ok := r["timeout"]; ok {
		if string(raw) == "null" {
			return 0, fmt.Errorf("invalid timeout")
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, err
		}
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 60 {
		return 0, fmt.Errorf("timeout must be finite and in (0, 60] seconds")
	}
	return time.Duration(v * float64(time.Second)), nil
}
func geometry(r request) (uint16, uint16, error) {
	rows, cols := 40, 120
	for key, target := range map[string]*int{"rows": &rows, "columns": &cols} {
		if raw, ok := r[key]; ok {
			if string(raw) == "null" {
				return 0, 0, fmt.Errorf("invalid terminal geometry")
			}
			if err := json.Unmarshal(raw, target); err != nil {
				return 0, 0, err
			}
		}
		if *target < 1 || *target > 65535 {
			return 0, 0, fmt.Errorf("terminal rows and columns must be integers in 1..65535")
		}
	}
	return uint16(rows), uint16(cols), nil
}

// Run executes one CLI invocation and returns its helper exit status.
func Run(args []string, output io.Writer) int {
	result := func() reply {
		if len(args) != 2 {
			return failure(fmt.Errorf("usage: caseyard-driver MODE JSON"), nil)
		}
		r, err := parse(args[1])
		if err != nil {
			return failure(err, nil)
		}
		return dispatch(args[0], r)
	}()
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return 1
	}
	if !result.OK {
		return 1
	}
	return 0
}

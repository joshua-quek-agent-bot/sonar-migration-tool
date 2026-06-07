package common

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// RuleImpact mirrors the {softwareQuality, severity} shape returned by the
// SonarQube /api/rules/search and /api/qualityprofiles/show APIs.
type RuleImpact struct {
	SoftwareQuality string
	Severity        string
}

// AcquireSem acquires a semaphore slot, respecting context cancellation.
func AcquireSem(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// EnrichRaw merges additional key-value pairs into a raw JSON object.
func EnrichRaw(raw json.RawMessage, metadata map[string]any) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		obj = make(map[string]json.RawMessage)
	}
	for k, v := range metadata {
		b, _ := json.Marshal(v)
		obj[k] = b
	}
	result, _ := json.Marshal(obj)
	return result
}

// EnrichAll applies EnrichRaw to every item in a slice.
func EnrichAll(items []json.RawMessage, metadata map[string]any) []json.RawMessage {
	out := make([]json.RawMessage, len(items))
	for i, item := range items {
		out[i] = EnrichRaw(item, metadata)
	}
	return out
}

// ExtractField extracts a string value from a json.RawMessage by key.
func ExtractField(raw json.RawMessage, key string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	val, ok := obj[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(val, &s); err != nil {
		return strings.Trim(string(val), "\"")
	}
	return s
}

// ExtractBool extracts a boolean value from a json.RawMessage by key.
func ExtractBool(raw json.RawMessage, key string) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false
	}
	val, ok := obj[key]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(val, &b); err != nil {
		return false
	}
	return b
}

// ExtractStringMap extracts a Sonar-style "params" or similar object whose
// JSON form is an array of {key, value} pairs (as returned by
// /api/rules/search) and returns it as a plain map[string]string. An empty
// map is returned if the key is missing or the payload is malformed.
func ExtractStringMap(raw json.RawMessage, key string) map[string]string {
	result := map[string]string{}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return result
	}
	val, ok := obj[key]
	if !ok {
		return result
	}
	var pairs []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(val, &pairs); err != nil {
		return result
	}
	for _, p := range pairs {
		result[p.Key] = p.Value
	}
	return result
}

// ExtractImpacts extracts a Sonar-style "impacts" array (as returned by
// /api/rules/search) into a slice of RuleImpact. An empty slice is returned
// if the key is missing or the payload is malformed.
func ExtractImpacts(raw json.RawMessage, key string) []RuleImpact {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	val, ok := obj[key]
	if !ok {
		return nil
	}
	var impacts []RuleImpact
	if err := json.Unmarshal(val, &impacts); err != nil {
		return nil
	}
	return impacts
}

// ExtractTime extracts a timestamp from a json.RawMessage by key and parses
// it. SonarQube APIs surface timestamps as ISO-8601 strings (RFC 3339 with
// or without a colon in the timezone offset, optionally with millisecond
// precision) and occasionally as epoch-millis integers. All common shapes
// are accepted. A zero time.Time is returned on missing/invalid input.
func ExtractTime(raw json.RawMessage, key string) time.Time {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return time.Time{}
	}
	val, ok := obj[key]
	if !ok {
		return time.Time{}
	}
	var s string
	if err := json.Unmarshal(val, &s); err == nil && s != "" {
		// Try Sonar's preferred formats first, then fall back to
		// standard RFC 3339. We try the more-specific formats before the
		// less-specific ones so that a string with nanos is preserved
		// (e.g. Sonar's "yyyy-MM-dd'T'HH:mm:ss±HHmm" and the RFC 3339
		// variant "yyyy-MM-dd'T'HH:mm:ss±HH:MM").
		formats := []string{
			"2006-01-02T15:04:05.999999999-0700", // nanos, no colon
			"2006-01-02T15:04:05.999999999Z0700", // nanos, no colon, 'Z'
			time.RFC3339Nano,
			"2006-01-02T15:04:05-0700", // Sonar default, no colon
			"2006-01-02T15:04:05Z0700", // Sonar default, no colon, 'Z'
			time.RFC3339,
		}
		for _, layout := range formats {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
		return time.Time{}
	}
	// Fall back to a numeric epoch (ms if > 1e12, else seconds).
	var n json.Number
	if err := json.Unmarshal(val, &n); err == nil {
		if i, err := n.Int64(); err == nil {
			if i > 1e12 {
				return time.UnixMilli(i).UTC()
			}
			return time.Unix(i, 0).UTC()
		}
	}
	return time.Time{}
}

// Expansion defines a set of values for cross-product iteration.
type Expansion struct {
	Key    string
	Values []string
}

// ExpandCombinations returns all combinations from a list of expansions.
func ExpandCombinations(expansions []Expansion) []map[string]string {
	if len(expansions) == 0 {
		return []map[string]string{{}}
	}
	first := expansions[0]
	rest := ExpandCombinations(expansions[1:])
	var result []map[string]string
	for _, val := range first.Values {
		for _, combo := range rest {
			m := make(map[string]string, len(combo)+1)
			for k, v := range combo {
				m[k] = v
			}
			m[first.Key] = val
			result = append(result, m)
		}
	}
	return result
}

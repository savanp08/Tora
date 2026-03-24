package handlers

import "testing"

func TestParseTaskCustomFieldsFromJSON(t *testing.T) {
	fields := parseTaskCustomFieldsFromJSON(`{"priority":"high","estimate":5}`)
	if len(fields) != 2 {
		t.Fatalf("expected 2 parsed fields, got %d", len(fields))
	}
	if fields["priority"] != "high" {
		t.Fatalf("expected priority=high, got %v", fields["priority"])
	}
}

func TestMergeTaskCustomFieldsMaps(t *testing.T) {
	existing := map[string]interface{}{
		"priority": "high",
		"owner":    "alex",
	}
	patch := map[string]interface{}{
		"priority": "low",
		"owner":    nil,
		"site":     "Block A",
	}

	merged := mergeTaskCustomFieldsMaps(existing, patch)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged fields, got %d", len(merged))
	}
	if merged["priority"] != "low" {
		t.Fatalf("expected updated priority=low, got %v", merged["priority"])
	}
	if _, exists := merged["owner"]; exists {
		t.Fatalf("expected owner to be removed")
	}
	if merged["site"] != "Block A" {
		t.Fatalf("expected site field to be added, got %v", merged["site"])
	}
}

func TestMarshalTaskCustomFieldsRejectsUnsupportedValues(t *testing.T) {
	_, err := marshalTaskCustomFields(map[string]interface{}{
		"bad": make(chan int),
	})
	if err == nil {
		t.Fatalf("expected marshalTaskCustomFields to fail for unsupported value types")
	}
}

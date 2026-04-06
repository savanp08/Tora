package handlers

import (
	"fmt"
	"testing"
)

func TestTaskSchemaAlterQueriesIncludeTaskNumber(t *testing.T) {
	tableName := "converse.tasks"
	queries := taskSchemaAlterQueries(tableName)
	want := fmt.Sprintf(`ALTER TABLE %s ADD task_number int`, tableName)

	for _, query := range queries {
		if query == want {
			return
		}
	}

	t.Fatalf("expected task schema alter queries to include %q, got %#v", want, queries)
}

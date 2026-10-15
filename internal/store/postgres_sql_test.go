package store

import "testing"

func TestSplitSQL(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	stmts := splitSQL(string(raw))
	if len(stmts) != 6 {
		t.Fatalf("statements = %d, want 6", len(stmts))
	}
	for _, stmt := range stmts {
		if stmt == "" {
			t.Fatal("empty statement")
		}
	}
}

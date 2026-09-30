package database

import "testing"

func TestMigrationsAreOrderedAndNonEmpty(t *testing.T) {
	migrations, err := migrations()
	if err != nil {
		t.Fatalf("migrations() error = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("migrations() returned no migrations")
	}
	for i, migration := range migrations {
		if migration.name == "" || migration.sql == "" {
			t.Fatalf("migration %d is incomplete: %#v", i, migration)
		}
		if i > 0 && migrations[i-1].name >= migration.name {
			t.Fatalf("migrations are not ordered: %q then %q", migrations[i-1].name, migration.name)
		}
	}
}

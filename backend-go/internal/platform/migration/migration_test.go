package migration

import "testing"

func TestMigrationChecksumIsStableAndContentSensitive(t *testing.T) {
	first := migrationChecksum([]byte("SELECT 1;"))
	second := migrationChecksum([]byte("SELECT 1;"))
	changed := migrationChecksum([]byte("SELECT 2;"))
	if first == "" || first != second {
		t.Fatalf("stable checksum mismatch: %q %q", first, second)
	}
	if first == changed {
		t.Fatal("different migration contents must have different checksums")
	}
}

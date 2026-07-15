package dataset

import (
	"strings"
	"testing"
)

func TestSnapshotChecksumIsStableAndVersionSensitive(t *testing.T) {
	sources := []SourceRef{
		{EvidenceSourceID: 1, ProjectID: 2, SourceType: "note", SourceID: 10, SourceVersion: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Visibility: "project"},
		{EvidenceSourceID: 2, ProjectID: 2, SourceType: "note_media", SourceID: 20, SourceVersion: 3, ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Visibility: "project"},
	}
	first := snapshotChecksum(4, 2, sources)
	second := snapshotChecksum(4, 2, append([]SourceRef(nil), sources...))
	if first == "" || first != second {
		t.Fatalf("stable checksum mismatch: %q %q", first, second)
	}
	sources[1].SourceVersion++
	if changed := snapshotChecksum(4, 2, sources); changed == first {
		t.Fatal("source version change must alter the snapshot checksum")
	}
}

func TestSnapshotChecksumDoesNotDependOnRegistryRowID(t *testing.T) {
	sources := []SourceRef{{
		EvidenceSourceID: 11,
		ProjectID:        3,
		SourceType:       "note",
		SourceID:         42,
		SourceVersion:    2,
		ContentHash:      strings.Repeat("a", 64),
		Visibility:       "public",
	}}
	first := snapshotChecksum(7, 3, sources)
	sources[0].EvidenceSourceID = 999
	second := snapshotChecksum(7, 3, sources)
	if first != second {
		t.Fatalf("registry row id changed manifest checksum: %s != %s", first, second)
	}
}

package simulator

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSinkWritesFinalizedArtifacts(t *testing.T) {
	preset, _ := PresetFor("smoke", 123, ScenarioMixed)
	preset.Config.Sessions = 25
	root := t.TempDir()
	sink, err := NewFileSink(root, "file_sink_test", false)
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}
	preset.Config.RunID = "file_sink_test"
	report, err := NewEngine().Generate(context.Background(), preset.Config, SyntheticDataset(10, 20, 2), sink)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	dir := filepath.Join(root, preset.Config.RunID)
	for _, name := range []string{"profiles.ndjson", "events.ndjson", "report.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, name+".tmp")); !os.IsNotExist(err) {
			t.Fatalf("temporary %s still exists", name)
		}
	}

	eventLines, err := countLines(filepath.Join(dir, "events.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if int64(eventLines) != report.Events {
		t.Fatalf("event lines=%d report=%d", eventLines, report.Events)
	}
	reportBytes, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored Report
	if err := json.Unmarshal(reportBytes, &stored); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if stored.RunID != report.RunID || stored.Events != report.Events {
		t.Fatalf("stored report=%+v generated=%+v", stored, report)
	}
}

func TestFileSinkRequiresReplaceForExistingRun(t *testing.T) {
	root := t.TempDir()
	first, err := NewFileSink(root, "same_run", false)
	if err != nil {
		t.Fatal(err)
	}
	first.Abort(context.Background(), nil)
	if _, err := NewFileSink(root, "same_run", false); err == nil {
		t.Fatal("NewFileSink() expected existing run error")
	}
	second, err := NewFileSink(root, "same_run", true)
	if err != nil {
		t.Fatalf("NewFileSink(replace) error = %v", err)
	}
	second.Abort(context.Background(), nil)
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	count := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count, scanner.Err()
}

package delegation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testGeneration = "0123456789abcdef01234567"

func fixtureReport() Report {
	return Report{Schema: Schema, Generation: testGeneration, SourceAsOf: "2026-10-28T12:00:00Z", DispositionAsOf: "2026-10-28T12:00:00Z",
		Window:     Window{Start: "2026-10-21T00:00:00Z", End: "2026-10-28T12:00:00Z"},
		Dispatches: []Dispatch{{ID: "a", Started: "2026-10-27T12:00:00Z", Root: Identity{Family: "gpt"}, Dispatcher: Identity{Family: "claude"}, Child: Identity{Family: "muse"}}},
		Coverage:   []Coverage{{DispatchID: "a", Evaluable: true}}, Contributions: []Contribution{}, Panels: []Panel{}, Evidence: []Evidence{}}
}

func writePublication(t *testing.T, root string, data []byte, modify func(*publication)) {
	t.Helper()
	file := "export/" + testGeneration + "/dashboard.json"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, file), data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	pub := publication{Schema: Schema, Generation: testGeneration, Status: "published", File: file, SHA: hex.EncodeToString(digest[:])}
	if modify != nil {
		modify(&pub)
	}
	encoded, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "publication.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReadPreservesUnknownModelsAndExplicitNoFindingCoverage(t *testing.T) {
	root := t.TempDir()
	data, _ := json.Marshal(fixtureReport())
	writePublication(t, root, data, nil)
	report, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Dispatches[0].Child.Model != nil || !report.Coverage[0].Evaluable || len(report.Contributions) != 0 {
		t.Fatal("unknown model or no-findings coverage changed")
	}
}

func TestPublicationRejectsWrongChecksumTraversalAndSchema(t *testing.T) {
	for name, modify := range map[string]func(*publication){
		"checksum":   func(p *publication) { p.SHA = strings.Repeat("0", 64) },
		"traversal":  func(p *publication) { p.File = "../secret.json" },
		"schema":     func(p *publication) { p.Schema = "future" },
		"incomplete": func(p *publication) { p.Status = "writing" },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			data, _ := json.Marshal(fixtureReport())
			writePublication(t, root, data, modify)
			if _, err := Read(root); err == nil {
				t.Fatal("unsafe publication accepted")
			}
		})
	}
}

func TestPublicationRejectsSymlinkEscapeAndMissingCoverage(t *testing.T) {
	root := t.TempDir()
	data, _ := json.Marshal(fixtureReport())
	writePublication(t, root, data, nil)
	file := filepath.Join(root, "export", testGeneration, "dashboard.json")
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err == nil {
		t.Fatal("escaped export root")
	}
	r := fixtureReport()
	r.Coverage = nil
	data, _ = json.Marshal(r)
	root = t.TempDir()
	writePublication(t, root, data, nil)
	if _, err := Read(root); err == nil {
		t.Fatal("missing denominator treated as empty coverage")
	}
}

func TestResponseDoesNotIncludeUnrecognizedRawContent(t *testing.T) {
	data, _ := json.Marshal(fixtureReport())
	var body map[string]interface{}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	body["prompt"] = "private-raw-sentinel"
	body["summary"] = map[string]string{"secret": "private-raw-sentinel"}
	data, _ = json.Marshal(body)
	root := t.TempDir()
	writePublication(t, root, data, nil)
	report, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "private-raw-sentinel") {
		t.Fatal("raw unknown content escaped typed response")
	}
}

func TestReportRejectsDanglingCoverageAndDuplicateDispatches(t *testing.T) {
	for _, mode := range []string{"dangling", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			r := fixtureReport()
			if mode == "dangling" {
				r.Coverage[0].DispatchID = "missing"
			} else {
				r.Dispatches = append(r.Dispatches, r.Dispatches[0])
			}
			root := t.TempDir()
			data, _ := json.Marshal(r)
			writePublication(t, root, data, nil)
			if _, err := Read(root); err == nil {
				t.Fatal("bad reference accepted")
			}
		})
	}
}

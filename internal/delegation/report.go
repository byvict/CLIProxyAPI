package delegation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	log "github.com/sirupsen/logrus"
)

const Schema = "t3-observatory/1.0"

type Identity struct {
	Family string  `json:"family"`
	Model  *string `json:"model"`
}

type Dispatch struct {
	ID          string   `json:"dispatch_id"`
	Project     string   `json:"project_id"`
	Started     string   `json:"started_at"`
	Status      string   `json:"status"`
	Role        string   `json:"role"`
	Round       *string  `json:"round"`
	Root        Identity `json:"root"`
	Dispatcher  Identity `json:"dispatcher"`
	Child       Identity `json:"child"`
	RootThread  string   `json:"root_thread_id"`
	ChildThread *string  `json:"child_thread_id"`
	Overhead    bool     `json:"audit_overhead"`
}

type Coverage struct {
	DispatchID  string   `json:"dispatch_id"`
	Evaluable   bool     `json:"evaluable"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type Discoverer struct {
	DispatchID string `json:"dispatch_id"`
	Family     string `json:"child_family"`
}

type Contribution struct {
	ID               string       `json:"contribution_id"`
	PanelID          *string      `json:"panel_id"`
	Label            string       `json:"label"`
	Kind             string       `json:"kind"`
	Severity         string       `json:"severity"`
	Accepted         *bool        `json:"accepted"`
	Applied          *bool        `json:"applied"`
	Disposition      string       `json:"disposition"`
	Discoverers      []Discoverer `json:"discoverers"`
	DecisionEvidence []string     `json:"decision_evidence_ids"`
	ArtifactEvidence []string     `json:"artifact_evidence_ids"`
}

type Panel struct {
	ID           string   `json:"panel_id"`
	Complete     bool     `json:"complete"`
	DispatchIDs  []string `json:"dispatch_ids"`
	Families     []string `json:"expected_families"`
	Scope        string   `json:"scope"`
	Independence string   `json:"independence_limit"`
}

type Evidence struct {
	ID     string `json:"evidence_id"`
	Kind   string `json:"kind"`
	Thread string `json:"thread_id,omitempty"`
	Item   string `json:"source_item_id,omitempty"`
	Source string `json:"source_id,omitempty"`
	Base   string `json:"base,omitempty"`
	Head   string `json:"head,omitempty"`
	SHA    string `json:"sha256,omitempty"`
}

type Window struct {
	Start string `json:"start_inclusive"`
	End   string `json:"end_inclusive"`
}

type Scope struct {
	Census      string `json:"dispatch_census"`
	Attribution string `json:"attribution_scope"`
}

type Report struct {
	Schema          string         `json:"schema_version"`
	Generation      string         `json:"generation_id"`
	Window          Window         `json:"window"`
	SourceAsOf      string         `json:"source_as_of"`
	DispositionAsOf string         `json:"disposition_as_of"`
	Scope           Scope          `json:"coverage"`
	Dispatches      []Dispatch     `json:"dispatches"`
	Coverage        []Coverage     `json:"attribution_coverage"`
	Contributions   []Contribution `json:"contributions"`
	Panels          []Panel        `json:"panels"`
	Evidence        []Evidence     `json:"evidence"`
}

type publication struct {
	Schema     string `json:"schema_version"`
	Generation string `json:"generation_id"`
	Status     string `json:"status"`
	File       string `json:"file"`
	SHA        string `json:"sha256"`
}

var generationPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

func readLimited(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			log.WithError(errClose).Debug("close delegation export")
		}
	}()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("delegation export exceeds size limit")
	}
	return data, nil
}

func Read(directory string) (Report, error) {
	var report Report
	root, err := os.OpenRoot(directory)
	if err != nil {
		return report, err
	}
	defer func() {
		if errClose := root.Close(); errClose != nil {
			log.WithError(errClose).Debug("close delegation directory")
		}
	}()
	manifest, err := readLimited(root, "publication.json", 64<<10)
	if err != nil {
		return report, err
	}
	var pub publication
	if err := json.Unmarshal(manifest, &pub); err != nil {
		return report, fmt.Errorf("invalid delegation publication")
	}
	if pub.Schema != Schema || pub.Status != "published" || !generationPattern.MatchString(pub.Generation) || pub.File != "export/"+pub.Generation+"/dashboard.json" {
		return report, fmt.Errorf("unsupported delegation publication")
	}
	data, err := readLimited(root, pub.File, 64<<20)
	if err != nil {
		return report, err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != pub.SHA {
		return report, fmt.Errorf("delegation publication checksum mismatch")
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, fmt.Errorf("invalid delegation report")
	}
	if report.Schema != Schema || report.Generation != pub.Generation || report.Dispatches == nil || report.Coverage == nil || report.Contributions == nil || report.Panels == nil || report.Evidence == nil {
		return Report{}, fmt.Errorf("incomplete delegation report")
	}
	for _, value := range []string{report.SourceAsOf, report.DispositionAsOf, report.Window.Start, report.Window.End} {
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			return Report{}, fmt.Errorf("invalid delegation snapshot time")
		}
	}
	if err := report.validateReferences(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (r Report) validateReferences() error {
	ids := map[string]bool{}
	for _, d := range r.Dispatches {
		if d.ID == "" || ids[d.ID] || d.Root.Family == "" || d.Dispatcher.Family == "" || d.Child.Family == "" {
			return fmt.Errorf("invalid delegation identity")
		}
		if _, err := time.Parse(time.RFC3339Nano, d.Started); err != nil {
			return fmt.Errorf("invalid delegation start time")
		}
		ids[d.ID] = true
	}
	covered := map[string]bool{}
	for _, c := range r.Coverage {
		if !ids[c.DispatchID] || covered[c.DispatchID] {
			return fmt.Errorf("invalid delegation coverage reference")
		}
		covered[c.DispatchID] = true
	}
	for _, c := range r.Contributions {
		if c.ID == "" || c.Kind == "" || len(c.Discoverers) == 0 {
			return fmt.Errorf("invalid delegation contribution")
		}
		for _, d := range c.Discoverers {
			if !ids[d.DispatchID] {
				return fmt.Errorf("unknown contribution discoverer")
			}
		}
	}
	for _, p := range r.Panels {
		if p.Complete && (len(p.DispatchIDs) != 3 || len(p.Families) != 3) {
			return fmt.Errorf("invalid complete delegation panel")
		}
		for _, id := range p.DispatchIDs {
			if !ids[id] {
				return fmt.Errorf("unknown panel dispatch")
			}
		}
	}
	panels := map[string]map[string]bool{}
	for _, p := range r.Panels {
		if p.ID == "" || panels[p.ID] != nil {
			return fmt.Errorf("duplicate or empty panel identity")
		}
		roster := map[string]bool{}
		families := map[string]bool{}
		for _, family := range p.Families {
			families[family] = true
		}
		if p.Complete && (len(families) != 3 || families["unknown"]) {
			return fmt.Errorf("complete panel requires three known families")
		}
		for _, id := range p.DispatchIDs {
			if roster[id] {
				return fmt.Errorf("duplicate panel member")
			}
			roster[id] = true
		}
		panels[p.ID] = roster
	}
	contributions := map[string]bool{}
	for _, c := range r.Contributions {
		if contributions[c.ID] {
			return fmt.Errorf("duplicate contribution identity")
		}
		contributions[c.ID] = true
		if c.Kind == "verification_artifact" && c.Applied != nil && *c.Applied {
			return fmt.Errorf("verification artifact has no source-code application")
		}
		if c.PanelID != nil {
			for _, d := range c.Discoverers {
				if !panels[*c.PanelID][d.DispatchID] {
					return fmt.Errorf("discoverer outside contribution panel")
				}
			}
		}
	}
	return nil
}

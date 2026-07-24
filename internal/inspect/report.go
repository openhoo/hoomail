package inspect

const (
	AnalysisVersion       = 1
	MaxHTMLNodes          = 100000
	MaxHTMLTokenBytes     = 1 << 20
	MaxResources          = 2000
	MaxFindings           = 2000
	MaxEvidencePerFinding = 20
	MaxReportBytes        = 4 << 20
)

type Input struct {
	Raw        []byte
	LegacyHTML *string
	LegacyText *string
	StoredSize int64
}

type Analysis struct {
	Version                 int      `json:"version"`
	State                   string   `json:"state"`
	ParsedThroughPath       *string  `json:"parsedThroughPath"`
	UnavailableRuleFamilies []string `json:"unavailableRuleFamilies"`
	Truncated               bool     `json:"truncated"`
}

type Summary struct {
	Fail         int `json:"fail"`
	Warning      int `json:"warning"`
	Advisory     int `json:"advisory"`
	Observed     int `json:"observed"`
	Pass         int `json:"pass"`
	NotEvaluated int `json:"notEvaluated"`
}

type Reference struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Evidence struct {
	Source     string  `json:"source"`
	Path       *string `json:"path,omitempty"`
	Field      *string `json:"field,omitempty"`
	Occurrence *int    `json:"occurrence,omitempty"`
	Line       *int    `json:"line,omitempty"`
	Value      *string `json:"value,omitempty"`
}

type Finding struct {
	ID                string     `json:"id"`
	Category          string     `json:"category"`
	Outcome           string     `json:"outcome"`
	Severity          string     `json:"severity"`
	Basis             string     `json:"basis"`
	Applicability     string     `json:"applicability"`
	Label             string     `json:"label"`
	Detail            string     `json:"detail"`
	Evidence          []Evidence `json:"evidence"`
	EvidenceTruncated bool       `json:"evidenceTruncated"`
	Reference         *Reference `json:"reference"`
}

type Resource struct {
	Kind            string  `json:"kind"`
	Path            *string `json:"path"`
	URL             string  `json:"url"`
	Text            string  `json:"text"`
	OccurrenceCount int     `json:"occurrenceCount"`
}

type MimeNode struct {
	Path        string     `json:"path"`
	ContentType string     `json:"contentType"`
	Charset     *string    `json:"charset"`
	Encoding    *string    `json:"encoding"`
	Disposition *string    `json:"disposition"`
	Filename    *string    `json:"filename"`
	ContentID   *string    `json:"contentId"`
	RawSize     *int       `json:"rawSize"`
	DecodedSize *int       `json:"decodedSize"`
	Children    []MimeNode `json:"children"`
}

type Report struct {
	Analysis  Analysis   `json:"analysis"`
	Summary   Summary    `json:"summary"`
	Findings  []Finding  `json:"findings"`
	Resources []Resource `json:"resources"`
	MIMETree  *MimeNode  `json:"mimeTree"`
}

package report

type run struct {
	ToolVersion  string  `json:"tool_version"`
	Status       string  `json:"status"`
	ErrorCode    *string `json:"error_code"`
	Mode         string  `json:"mode"`
	GoVersion    string  `json:"go_version"`
	OS           string  `json:"os"`
	DurationMS   int64   `json:"duration_ms"`
	BaseProfile  string  `json:"base_pprof,omitempty"`
	ScaleProfile string  `json:"scaled_pprof,omitempty"`
	PathsMethod  string  `json:"paths_method,omitempty"`
}

type hotspot struct {
	Rank                int     `json:"rank"`
	Function            string  `json:"function"`
	File                string  `json:"file"`
	BaselineSelfSeconds float64 `json:"baseline_self_seconds"`
	ScaledSelfSeconds   float64 `json:"scaled_self_seconds"`
	DeltaSelfSeconds    float64 `json:"delta_self_seconds"`
	NDC                 float64 `json:"ndc"`
	BaselineSamples     int64   `json:"baseline_samples"`
	ScaledSamples       int64   `json:"scaled_samples"`
	BaselineOccurrences *int    `json:"baseline_occurrences,omitempty"`
	ScaledOccurrences   *int    `json:"scaled_occurrences,omitempty"`

	// Seconds per benchmark operation: present when the hotspots were ranked
	// per operation (benchmark mode).
	BaselinePerOpSeconds *float64 `json:"baseline_per_op_seconds,omitempty"`
	ScaledPerOpSeconds   *float64 `json:"scaled_per_op_seconds,omitempty"`
	DeltaPerOpSeconds    *float64 `json:"delta_per_op_seconds,omitempty"`
	NDCPerOp             *float64 `json:"ndc_per_op,omitempty"`
}

type criticalPath struct {
	Target     string  `json:"target"`
	TargetRank int     `json:"target_rank"`
	Signature  string  `json:"signature"`
	Score      float64 `json:"score"`
	Truncated  bool    `json:"truncated"`
	Frames     []frame `json:"frames"`
}

type frame struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type document struct {
	SchemaVersion int            `json:"schema_version"`
	NamesHashed   bool           `json:"names_hashed"`
	Run           run            `json:"run"`
	Hotspots      []hotspot      `json:"hotspots"`
	CriticalPaths []criticalPath `json:"critical_paths"`
}

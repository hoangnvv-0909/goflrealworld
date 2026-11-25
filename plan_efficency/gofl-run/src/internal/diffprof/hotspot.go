package diffprof

import "math"

// Hotspot is one element of hotspots.json: section 3.2 of data contract v0,
// then four optional fields that only exist when run.json has a bench object
// (time per operation). A reader that follows v0 ignores the extra fields.
type Hotspot struct {
	Rank                int     `json:"rank"`
	Function            string  `json:"function"`
	File                string  `json:"file"`
	BaselineSelfSeconds float64 `json:"baseline_self_seconds"`
	ScaledSelfSeconds   float64 `json:"scaled_self_seconds"`
	DeltaSelfSeconds    float64 `json:"delta_self_seconds"`
	NDC                 float64 `json:"ndc"`
	BaselineSamples     int64   `json:"baseline_samples"`
	ScaledSamples       int64   `json:"scaled_samples"`
	BaselineOccurrences int     `json:"baseline_occurrences"`
	ScaledOccurrences   int     `json:"scaled_occurrences"`

	BaselinePerOpSeconds *float64 `json:"baseline_per_op_seconds,omitempty"`
	ScaledPerOpSeconds   *float64 `json:"scaled_per_op_seconds,omitempty"`
	DeltaPerOpSeconds    *float64 `json:"delta_per_op_seconds,omitempty"`
	NDCPerOp             *float64 `json:"ndc_per_op,omitempty"`
}

// round rounds x to the given number of decimal places.
func round(x float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(x*p) / p
}

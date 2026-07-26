package output

import (
	"encoding/json"
	"testing"
)

// TestOKHeadMarshal is the tracer: an OK head marshals to exactly the
// schema_version and ok pair that opens every result envelope.
func TestOKHeadMarshal(t *testing.T) {
	b, err := json.Marshal(OKHead())
	if err != nil {
		t.Fatalf("marshal OKHead: %v", err)
	}
	if got, want := string(b), `{"schema_version":1,"ok":true}`; got != want {
		t.Errorf("OKHead marshaled to %s, want %s", got, want)
	}
}

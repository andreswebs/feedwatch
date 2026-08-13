package feedwatch

// SchemaVersion is the version of the output contract, bumped on breaking
// shape changes to any result envelope (docs/adr/0005-output-contract.md).
const SchemaVersion = 1

// Head opens every result envelope: schema_version identifies the output
// contract version and ok reports whether the invocation succeeded. Embed it as
// the first field of each result struct so the two keys lead every result a
// frontend renders.
type Head struct {
	SchemaVersion int  `json:"schema_version"`
	OK            bool `json:"ok"`
}

// OKHead returns the head for a successful result: the current schema version
// and ok true.
func OKHead() Head { return Head{SchemaVersion: SchemaVersion, OK: true} }

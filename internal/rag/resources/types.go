package resources

// Mode identifies how a resource set is injected into a prompt (REQ-01).
// A set must declare one of these two values; there is no default.
const (
	ModeFull      = "full"
	ModeRetrieval = "retrieval"
)

// Set is one resource-set declaration from resources.yaml, after per-system
// overlay merge and path resolution (REQ-01).
type Set struct {
	Name  string
	Tags  []string
	Mode  string
	Paths []string
	// Include constrains indexed files to matching patterns (REQ-03 / T28).
	Include []string
	// Exclude removes matching files from indexing (REQ-03 / T28).
	Exclude []string
}

// RejectedPath names one declared path that was refused during resolution
// (REQ-11: path escapes must be individually observable).
type RejectedPath struct {
	Set    string
	Path   string
	Reason string
}

// Registry is the result of Load: the ordered, merged resource sets plus
// any paths that were rejected during resolution.
type Registry struct {
	Sets          []Set
	RejectedPaths []RejectedPath
}

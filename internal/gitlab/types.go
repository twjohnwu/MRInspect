package gitlab

type Author struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

type MergeRequest struct {
	ID           int    `json:"id"`
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Author       Author `json:"author"`
	ChangesCount string `json:"changes_count"`
	WebURL       string `json:"web_url"`
}

type Change struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	AMode       string `json:"a_mode"`
	BMode       string `json:"b_mode"`
}

// Truncated reports whether GitLab returned an empty diff for a file whose
// content really changed (diff size limits apply). Pure renames, deletions
// and mode-only changes legitimately carry an empty diff and are not flagged.
func (c Change) Truncated() bool {
	if c.Diff != "" || c.RenamedFile || c.DeletedFile {
		return false
	}
	modeOnly := !c.NewFile && c.AMode != "" && c.BMode != "" && c.AMode != c.BMode
	return !modeOnly
}

type MRChangesResponse struct {
	Changes  []Change `json:"changes"`
	Overflow bool     `json:"overflow"`
}

type Note struct {
	ID     int    `json:"id"`
	Body   string `json:"body"`
	Author Author `json:"author"`
}

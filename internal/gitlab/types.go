package gitlab

type Author struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

type MergeRequest struct {
	ID           int      `json:"id"`
	IID          int      `json:"iid"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	Author       Author   `json:"author"`
	ChangesCount string   `json:"changes_count"`
	WebURL       string   `json:"web_url"`
	SHA          string   `json:"sha"`
	DiffRefs     DiffRefs `json:"diff_refs"`
}

// DiffRefs carries the commit anchors GitLab reports for a merge request.
type DiffRefs struct {
	BaseSHA string `json:"base_sha"`
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
	TooLarge    bool   `json:"too_large"`
	Collapsed   bool   `json:"collapsed"`
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
	SHA      string   `json:"sha"`
}

// CompareResponse is the repository compare payload; CompareTimeout marks a
// partial result when GitLab timed out computing the diffs.
type CompareResponse struct {
	Diffs          []Change `json:"diffs"`
	CompareTimeout bool     `json:"compare_timeout"`
}

type Note struct {
	ID        int    `json:"id"`
	Body      string `json:"body"`
	Author    Author `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

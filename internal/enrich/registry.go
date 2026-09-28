package enrich

// Registry maps a tool name to its Tool implementation.
type Registry map[string]Tool

// Default returns the registry of built-in tools (repo_search, read_file_ranges).
func Default() Registry {
	return Registry{
		"repo_search":      &searchTool{},
		"read_file_ranges": &readRangeTool{},
	}
}

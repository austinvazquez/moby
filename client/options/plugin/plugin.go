package plugin

// CreateOptions hold all options to plugin create.
type CreateOptions struct {
	RepoName string
}

// DisableOptions holds parameters to disable plugins.
type DisableOptions struct {
	Force bool
}

package client

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/moby/moby/client/options/plugin"
)

// PluginCreate creates a plugin
func (cli *Client) PluginCreate(ctx context.Context, createContext io.Reader, createOptions plugin.CreateOptions) error {
	headers := http.Header(make(map[string][]string))
	headers.Set("Content-Type", "application/x-tar")

	query := url.Values{}
	query.Set("name", createOptions.RepoName)

	resp, err := cli.postRaw(ctx, "/plugins/create", query, createContext, headers)
	defer ensureReaderClosed(resp)
	return err
}

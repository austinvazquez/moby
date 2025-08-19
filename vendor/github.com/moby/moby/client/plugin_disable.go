package client

import (
	"context"
	"net/url"

	"github.com/moby/moby/client/options/plugin"
)

// PluginDisable disables a plugin
func (cli *Client) PluginDisable(ctx context.Context, name string, options plugin.DisableOptions) error {
	name, err := trimID("plugin", name)
	if err != nil {
		return err
	}
	query := url.Values{}
	if options.Force {
		query.Set("force", "1")
	}
	resp, err := cli.post(ctx, "/plugins/"+name+"/disable", query, nil, nil)
	defer ensureReaderClosed(resp)
	return err
}

package images

import (
	"context"
	"errors"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/daemon/internal/layer"
	"github.com/docker/docker/daemon/internal/metrics"
	"github.com/docker/docker/daemon/server/backend"
	"github.com/moby/moby/api/types/image"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ImageHistory returns a slice of ImageHistory structures for the specified image
// name by walking the image lineage.
func (i *ImageService) ImageHistory(ctx context.Context, name string, platform *ocispec.Platform) ([]*image.HistoryResponseItem, error) {
	start := time.Now()
	img, err := i.GetImage(ctx, name, backend.GetImageOpts{Platform: platform})
	if err != nil {
		return nil, err
	}

	history := []*image.HistoryResponseItem{}

	layerCounter := 0
	rootFS := *img.RootFS
	rootFS.DiffIDs = nil

	for _, h := range img.History {
		var layerSize int64

		if !h.EmptyLayer {
			if len(img.RootFS.DiffIDs) <= layerCounter {
				return nil, errors.New("too many non-empty layers in History section")
			}
			rootFS.Append(img.RootFS.DiffIDs[layerCounter])
			l, err := i.layerStore.Get(rootFS.ChainID())
			if err != nil {
				return nil, err
			}
			layerSize = l.DiffSize()
			layer.ReleaseAndLog(i.layerStore, l)
			layerCounter++
		}

		var created int64
		if h.Created != nil {
			created = h.Created.Unix()
		}

		history = append([]*image.HistoryResponseItem{{
			ID:        "<missing>",
			Created:   created,
			CreatedBy: h.CreatedBy,
			Comment:   h.Comment,
			Size:      layerSize,
		}}, history...)
	}

	// Fill in image IDs and tags
	histImg := img
	id := img.ID()
	for _, h := range history {
		h.ID = id.String()

		var tags []string
		for _, r := range i.referenceStore.References(id.Digest()) {
			if _, ok := r.(reference.NamedTagged); ok {
				tags = append(tags, reference.FamiliarString(r))
			}
		}

		h.Tags = tags

		id = histImg.Parent
		if id == "" {
			break
		}
		histImg, err = i.GetImage(ctx, id.String(), backend.GetImageOpts{})
		if err != nil {
			break
		}
	}
	metrics.ImageActions.WithValues("history").UpdateSince(start)
	return history, nil
}

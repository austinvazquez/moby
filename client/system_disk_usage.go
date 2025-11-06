package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client/pkg/versions"
)

// DiskUsageOptions holds parameters for [Client.DiskUsage] operations.
type DiskUsageOptions struct {
	// Containers controls whether container disk usage should be computed.
	Containers bool

	// Images controls whether image disk usage should be computed.
	Images bool

	// BuildCache controls whether build cache disk usage should be computed.
	BuildCache bool

	// Volumes controls whether volume disk usage should be computed.
	Volumes bool

	// Verbose enables more detailed disk usage information.
	Verbose bool
}

// DiskUsageResult is the result of [Client.DiskUsage] operations.
type DiskUsageResult struct {
	// Containers holds container disk usage information.
	Containers ContainersDiskUsage

	// Images holds image disk usage information.
	Images ImagesDiskUsage

	// BuildCache holds build cache disk usage information.
	BuildCache BuildCacheDiskUsage

	// Volumes holds volume disk usage information.
	Volumes VolumesDiskUsage
}

// ContainersDiskUsage contains disk usage information for containers.
type ContainersDiskUsage struct {
	// ActiveContainers is the number of active containers.
	ActiveContainers int64

	// TotalContainers is the total number of containers.
	TotalContainers int64

	// Reclaimable is the amount of disk space that can be reclaimed.
	Reclaimable int64

	// TotalSize is the total disk space used by all containers.
	TotalSize int64

	// Items holds detailed information about each container.
	Items []container.Summary
}

// ImagesDiskUsage contains disk usage information for images.
type ImagesDiskUsage struct {
	// ActiveImages is the number of active images.
	ActiveImages int64

	// TotalImages is the total number of images.
	TotalImages int64

	// Reclaimable is the amount of disk space that can be reclaimed.
	Reclaimable int64

	// TotalSize is the total disk space used by all images.
	TotalSize int64

	// Items holds detailed information about each image.
	Items []image.Summary
}

// VolumesDiskUsage contains disk usage information for volumes.
type VolumesDiskUsage struct {
	// ActiveVolumes is the number of active volumes.
	ActiveVolumes int64

	// TotalVolumes is the total number of volumes.
	TotalVolumes int64

	// Reclaimable is the amount of disk space that can be reclaimed.
	Reclaimable int64

	// TotalSize is the total disk space used by all volumes.
	TotalSize int64

	// Items holds detailed information about each volume.
	Items []volume.Volume
}

// BuildCacheDiskUsage contains disk usage information for build cache.
type BuildCacheDiskUsage struct {
	// ActiveBuildCacheRecords is the number of active build cache records.
	ActiveBuildCacheRecords int64

	// TotalBuildCacheRecords is the total number of build cache records.
	TotalBuildCacheRecords int64

	// Reclaimable is the amount of disk space that can be reclaimed.
	Reclaimable int64

	// TotalSize is the total disk space used by all build cache records.
	TotalSize int64

	// Items holds detailed information about each build cache record.
	Items []build.CacheRecord
}

// DiskUsage requests the current data usage from the daemon.
func (cli *Client) DiskUsage(ctx context.Context, options DiskUsageOptions) (DiskUsageResult, error) {
	query := url.Values{}

	for _, t := range []struct {
		flag   bool
		sysObj system.DiskUsageObject
	}{
		{options.Containers, system.ContainerObject},
		{options.Images, system.ImageObject},
		{options.Volumes, system.VolumeObject},
		{options.BuildCache, system.BuildCacheObject},
	} {
		if t.flag {
			query.Add("type", string(t.sysObj))
		}
	}

	if options.Verbose {
		query.Set("verbose", "1")
	}

	resp, err := cli.get(ctx, "/system/df", query, nil)
	defer ensureReaderClosed(resp)
	if err != nil {
		return DiskUsageResult{}, err
	}

	var du system.DiskUsage
	if err := json.NewDecoder(resp.Body).Decode(&du); err != nil {
		return DiskUsageResult{}, fmt.Errorf("Error retrieving disk usage: %v", err)
	}

	// Generate result from a legacy response.
	if versions.LessThan(cli.version, "1.52") {
		return diskUsageResultFromLegacyAPI(&du), nil
	}

	var r DiskUsageResult
	if idu := du.ImageUsage; idu != nil {
		r.Images = ImagesDiskUsage{
			ActiveImages: idu.ActiveImages,
			Reclaimable:  idu.Reclaimable,
			TotalImages:  idu.TotalImages,
			TotalSize:    idu.TotalSize,
		}

		if options.Verbose {
			r.Images.Items = slices.Clone(idu.Items)
		}
	}

	if cdu := du.ContainerUsage; cdu != nil {
		r.Containers = ContainersDiskUsage{
			ActiveContainers: cdu.ActiveContainers,
			Reclaimable:      cdu.Reclaimable,
			TotalContainers:  cdu.TotalContainers,
			TotalSize:        cdu.TotalSize,
		}

		if options.Verbose {
			r.Containers.Items = slices.Clone(cdu.Items)
		}
	}

	if bdu := du.BuildCacheUsage; bdu != nil {
		r.BuildCache = BuildCacheDiskUsage{
			ActiveBuildCacheRecords: bdu.ActiveBuildCacheRecords,
			Reclaimable:             bdu.Reclaimable,
			TotalBuildCacheRecords:  bdu.TotalBuildCacheRecords,
			TotalSize:               bdu.TotalSize,
		}

		if options.Verbose {
			r.BuildCache.Items = slices.Clone(bdu.Items)
		}
	}

	if vdu := du.VolumeUsage; vdu != nil {
		r.Volumes = VolumesDiskUsage{
			ActiveVolumes: vdu.ActiveVolumes,
			Reclaimable:   vdu.Reclaimable,
			TotalVolumes:  vdu.TotalVolumes,
			TotalSize:     vdu.TotalSize,
		}

		if options.Verbose {
			r.Volumes.Items = slices.Clone(vdu.Items)
		}
	}

	return r, nil
}

func diskUsageResultFromLegacyAPI(du *system.DiskUsage) DiskUsageResult {
	return DiskUsageResult{
		Images:     imageDiskUsageFromLegacyAPI(du),
		Containers: containerDiskUsageFromLegacyAPI(du),
		BuildCache: buildCacheDiskUsageFromLegacyAPI(du),
		Volumes:    volumeDiskUsageFromLegacyAPI(du),
	}
}

func imageDiskUsageFromLegacyAPI(du *system.DiskUsage) ImagesDiskUsage {
	var (
		reclaimable  int64
		activeImages int64
	)

	for _, i := range du.Images {
		if i.Containers > 0 {
			activeImages++
		} else if i.Size >= 0 && i.SharedSize >= 0 {
			reclaimable += i.Size - i.SharedSize
		}
	}

	return ImagesDiskUsage{
		ActiveImages: activeImages,
		TotalImages:  int64(len(du.Images)),
		TotalSize:    du.LayersSize,
		Reclaimable:  reclaimable,
		Items:        du.Images,
	}
}

func containerDiskUsageFromLegacyAPI(du *system.DiskUsage) ContainersDiskUsage {
	var (
		activeContainers int64
		totalSize        int64
		used             int64
	)

	for _, c := range du.Containers {
		totalSize += c.SizeRw
		switch strings.ToLower(c.State) {
		case "running", "paused", "restarting":
			activeContainers++
			used += c.SizeRw
		}
	}

	return ContainersDiskUsage{
		ActiveContainers: activeContainers,
		TotalContainers:  int64(len(du.Containers)),
		TotalSize:        totalSize,
		Reclaimable:      totalSize - used,
		Items:            du.Containers,
	}
}

func buildCacheDiskUsageFromLegacyAPI(du *system.DiskUsage) BuildCacheDiskUsage {
	var (
		activeRecords int64
		totalSize     int64
		used          int64
	)

	for _, b := range du.BuildCache {
		totalSize += b.Size

		if b.InUse {
			activeRecords++
			used += b.Size
		}
	}

	return BuildCacheDiskUsage{
		ActiveBuildCacheRecords: activeRecords,
		TotalBuildCacheRecords:  int64(len(du.BuildCache)),
		TotalSize:               totalSize,
		Reclaimable:             totalSize - used,
		Items:                   du.BuildCache,
	}
}

func volumeDiskUsageFromLegacyAPI(du *system.DiskUsage) VolumesDiskUsage {
	var (
		activeVolumes int64
		totalSize     int64
		used          int64
	)

	for _, v := range du.Volumes {
		// Ignore volumes with no usage data
		if v.UsageData != nil {
			if v.UsageData.RefCount > 0 {
				activeVolumes++
				used += v.UsageData.Size
			}
			if v.UsageData.Size > 0 {
				totalSize += v.UsageData.Size
			}
		}
	}

	return VolumesDiskUsage{
		ActiveVolumes: activeVolumes,
		TotalVolumes:  int64(len(du.Volumes)),
		TotalSize:     totalSize,
		Reclaimable:   totalSize - used,
		Items:         du.Volumes,
	}
}

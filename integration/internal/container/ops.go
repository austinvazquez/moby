package container

import (
	"maps"
	"slices"
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ConfigOpt is an option to apply to a container.
type ConfigOpt func(*TestContainerConfig)

// WithName sets the name of the container
func WithName(name string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Name = name
	}
}

// WithHostname sets the hostname of the container
func WithHostname(name string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.Hostname = name
	}
}

// WithLinks sets the links of the container
func WithLinks(links ...string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Links = links
	}
}

// WithImage sets the image of the container
func WithImage(image string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.Image = image
	}
}

// WithCmd sets the commands of the container
func WithCmd(cmds ...string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.Cmd = cmds
	}
}

// WithNetworkMode sets the network mode of the container
func WithNetworkMode(mode string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.NetworkMode = container.NetworkMode(mode)
	}
}

// WithDNS sets external DNS servers for the container
func WithDNS(dns []string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.DNS = append([]string(nil), dns...)
	}
}

// WithSysctls sets sysctl options for the container
func WithSysctls(sysctls map[string]string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Sysctls = maps.Clone(sysctls)
	}
}

// WithExposedPorts sets the exposed ports of the container
func WithExposedPorts(ports ...string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.ExposedPorts = map[nat.Port]struct{}{}
		for _, port := range ports {
			c.Config.ExposedPorts[nat.Port(port)] = struct{}{}
		}
	}
}

// WithPortMap sets/replaces port mappings.
func WithPortMap(pm nat.PortMap) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.PortBindings = nat.PortMap{}
		for p, b := range pm {
			c.HostConfig.PortBindings[p] = slices.Clone(b)
		}
	}
}

// WithTty sets the TTY mode of the container
func WithTty(tty bool) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.Tty = tty
	}
}

// WithWorkingDir sets the working dir of the container
func WithWorkingDir(dir string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.WorkingDir = dir
	}
}

// WithMount adds an mount
func WithMount(m mount.Mount) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, m)
	}
}

// WithVolume sets the volume of the container
func WithVolume(target string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.Config.Volumes == nil {
			c.Config.Volumes = map[string]struct{}{}
		}
		c.Config.Volumes[target] = struct{}{}
	}
}

// WithBind sets the bind mount of the container
func WithBind(src, target string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Binds = append(c.HostConfig.Binds, src+":"+target)
	}
}

// WithBindRaw sets the bind mount of the container
func WithBindRaw(s string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Binds = append(c.HostConfig.Binds, s)
	}
}

// WithTmpfs sets a target path in the container to a tmpfs, with optional options
// (separated with a colon).
func WithTmpfs(targetAndOpts string) func(config *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.HostConfig.Tmpfs == nil {
			c.HostConfig.Tmpfs = make(map[string]string)
		}

		target, opts, _ := strings.Cut(targetAndOpts, ":")
		c.HostConfig.Tmpfs[target] = opts
	}
}

func WithMacAddress(networkName, mac string) func(config *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.NetworkingConfig.EndpointsConfig == nil {
			c.NetworkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{}
		}
		if v, ok := c.NetworkingConfig.EndpointsConfig[networkName]; !ok || v == nil {
			c.NetworkingConfig.EndpointsConfig[networkName] = &network.EndpointSettings{}
		}
		c.NetworkingConfig.EndpointsConfig[networkName].MacAddress = mac
	}
}

// WithIPv4 sets the specified ip for the specified network of the container
func WithIPv4(networkName, ip string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.NetworkingConfig.EndpointsConfig == nil {
			c.NetworkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{}
		}
		if v, ok := c.NetworkingConfig.EndpointsConfig[networkName]; !ok || v == nil {
			c.NetworkingConfig.EndpointsConfig[networkName] = &network.EndpointSettings{}
		}
		if c.NetworkingConfig.EndpointsConfig[networkName].IPAMConfig == nil {
			c.NetworkingConfig.EndpointsConfig[networkName].IPAMConfig = &network.EndpointIPAMConfig{}
		}
		c.NetworkingConfig.EndpointsConfig[networkName].IPAMConfig.IPv4Address = ip
	}
}

// WithIPv6 sets the specified ip6 for the specified network of the container
func WithIPv6(networkName, ip string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.NetworkingConfig.EndpointsConfig == nil {
			c.NetworkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{}
		}
		if v, ok := c.NetworkingConfig.EndpointsConfig[networkName]; !ok || v == nil {
			c.NetworkingConfig.EndpointsConfig[networkName] = &network.EndpointSettings{}
		}
		if c.NetworkingConfig.EndpointsConfig[networkName].IPAMConfig == nil {
			c.NetworkingConfig.EndpointsConfig[networkName].IPAMConfig = &network.EndpointIPAMConfig{}
		}
		c.NetworkingConfig.EndpointsConfig[networkName].IPAMConfig.IPv6Address = ip
	}
}

func WithEndpointSettings(nw string, config *network.EndpointSettings) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.NetworkingConfig.EndpointsConfig == nil {
			c.NetworkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{}
		}
		if _, ok := c.NetworkingConfig.EndpointsConfig[nw]; !ok {
			c.NetworkingConfig.EndpointsConfig[nw] = config
		}
	}
}

// WithLogDriver sets the log driver to use for the container
func WithLogDriver(driver string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.LogConfig.Type = driver
	}
}

// WithAutoRemove sets the container to be removed on exit
func WithAutoRemove(c *TestContainerConfig) {
	c.HostConfig.AutoRemove = true
}

// WithPidsLimit sets the container's "pids-limit
func WithPidsLimit(limit *int64) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.HostConfig == nil {
			c.HostConfig = &container.HostConfig{}
		}
		c.HostConfig.PidsLimit = limit
	}
}

// WithRestartPolicy sets container's restart policy
func WithRestartPolicy(policy container.RestartPolicyMode) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.RestartPolicy.Name = policy
	}
}

// WithUser sets the user
func WithUser(user string) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.User = user
	}
}

// WithAdditionalGroups sets the additional groups for the container
func WithAdditionalGroups(groups ...string) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.GroupAdd = groups
	}
}

// WithPrivileged sets privileged mode for the container
func WithPrivileged(privileged bool) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.HostConfig == nil {
			c.HostConfig = &container.HostConfig{}
		}
		c.HostConfig.Privileged = privileged
	}
}

// WithCgroupnsMode sets the cgroup namespace mode for the container
func WithCgroupnsMode(mode string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		if c.HostConfig == nil {
			c.HostConfig = &container.HostConfig{}
		}
		c.HostConfig.CgroupnsMode = container.CgroupnsMode(mode)
	}
}

// WithExtraHost sets the user defined IP:Host mappings in the container's
// /etc/hosts file
func WithExtraHost(extraHost string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.ExtraHosts = append(c.HostConfig.ExtraHosts, extraHost)
	}
}

// WithPlatform specifies the desired platform the image should have.
func WithPlatform(p *ocispec.Platform) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Platform = p
	}
}

// WithWindowsDevice specifies a Windows Device, ala `--device` on the CLI
func WithWindowsDevice(device string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Devices = append(c.HostConfig.Devices, container.DeviceMapping{PathOnHost: device})
	}
}

// WithIsolation specifies the isolation technology to apply to the container
func WithIsolation(isolation container.Isolation) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Isolation = isolation
	}
}

// WithConsoleSize sets the initial console size of the container
func WithConsoleSize(width, height uint) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.ConsoleSize = [2]uint{height, width}
	}
}

// WithAnnotations set the annotations for the container.
func WithAnnotations(annotations map[string]string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Annotations = annotations
	}
}

// WithRuntime sets the runtime to use to start the container
func WithRuntime(name string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.Runtime = name
	}
}

// WithCDIDevices sets the CDI devices to use to start the container
func WithCDIDevices(cdiDeviceNames ...string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		request := container.DeviceRequest{
			Driver:    "cdi",
			DeviceIDs: cdiDeviceNames,
		}
		c.HostConfig.DeviceRequests = append(c.HostConfig.DeviceRequests, request)
	}
}

func WithCapability(capabilities ...string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.CapAdd = append(c.HostConfig.CapAdd, capabilities...)
	}
}

func WithDropCapability(capabilities ...string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.CapDrop = append(c.HostConfig.CapDrop, capabilities...)
	}
}

func WithSecurityOpt(opt string) func(*TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.SecurityOpt = append(c.HostConfig.SecurityOpt, opt)
	}
}

// WithPIDMode sets the PID-mode for the container.
func WithPIDMode(mode container.PidMode) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig.PidMode = mode
	}
}

func WithStopSignal(stopSignal string) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.StopSignal = stopSignal
	}
}

func WithContainerWideMacAddress(address string) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.Config.MacAddress = address //nolint:staticcheck // ignore SA1019: field is deprecated, but still used on API < v1.44.
	}
}

// WithHostConfig sets a custom [container.HostConfig] for the container.
func WithHostConfig(hc *container.HostConfig) func(c *TestContainerConfig) {
	return func(c *TestContainerConfig) {
		c.HostConfig = hc
	}
}

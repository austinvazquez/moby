package container

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	realcontainer "github.com/docker/docker/daemon/container"
	"github.com/docker/docker/integration/internal/container"
	"github.com/docker/docker/testutil"
	"github.com/docker/docker/testutil/daemon"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"golang.org/x/sys/unix"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/assert/opt"
	"gotest.tools/v3/skip"
)

// This is a regression test for #36145
// It ensures that a container can be started when the daemon was improperly
// shutdown when the daemon is brought back up.
//
// The regression is due to improper error handling preventing a container from
// being restored and as such have the resources cleaned up.
//
// To test this, we need to kill dockerd, then kill both the containerd-shim and
// the container process, then start dockerd back up and attempt to start the
// container again.
func TestContainerStartOnDaemonRestart(t *testing.T) {
	skip.If(t, testEnv.IsRemoteDaemon, "cannot start daemon on remote test run")
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")
	skip.If(t, testEnv.IsRootless)
	t.Parallel()

	ctx := testutil.StartSpan(baseContext, t)

	d := daemon.New(t)
	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false")
	defer d.Stop(t)

	c := d.NewClientT(t)

	cID := container.Create(ctx, t, c)
	defer c.ContainerRemove(ctx, cID, containertypes.RemoveOptions{Force: true})

	err := c.ContainerStart(ctx, cID, containertypes.StartOptions{})
	assert.Check(t, err, "error starting test container")

	inspect, err := c.ContainerInspect(ctx, cID)
	assert.Check(t, err, "error getting inspect data")

	ppid := getContainerdShimPid(t, inspect)

	err = d.Kill()
	assert.Check(t, err, "failed to kill test daemon")

	err = unix.Kill(inspect.State.Pid, unix.SIGKILL)
	assert.Check(t, err, "failed to kill container process")

	err = unix.Kill(ppid, unix.SIGKILL)
	assert.Check(t, err, "failed to kill containerd-shim")

	d.Start(t, "--iptables=false", "--ip6tables=false")

	err = c.ContainerStart(ctx, cID, containertypes.StartOptions{})
	assert.Check(t, err, "failed to start test container")
}

func getContainerdShimPid(t *testing.T, c containertypes.InspectResponse) int {
	statB, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", c.State.Pid))
	assert.Check(t, err, "error looking up containerd-shim pid")

	// ppid is the 4th entry in `/proc/pid/stat`
	ppid, err := strconv.Atoi(strings.Fields(string(statB))[3])
	assert.Check(t, err, "error converting ppid field to int")

	assert.Check(t, ppid != 1, "got unexpected ppid")
	return ppid
}

// TestDaemonRestartIpcMode makes sure a container keeps its ipc mode
// (derived from daemon default) even after the daemon is restarted
// with a different default ipc mode.
func TestDaemonRestartIpcMode(t *testing.T) {
	skip.If(t, testEnv.IsRemoteDaemon, "cannot start daemon on remote test run")
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")
	t.Parallel()

	ctx := testutil.StartSpan(baseContext, t)

	d := daemon.New(t)
	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false", "--default-ipc-mode=private")
	defer d.Stop(t)

	c := d.NewClientT(t)

	// check the container is created with private ipc mode as per daemon default
	cID := container.Run(ctx, t, c,
		container.WithCmd("top"),
		container.WithRestartPolicy(containertypes.RestartPolicyAlways),
	)
	defer c.ContainerRemove(ctx, cID, containertypes.RemoveOptions{Force: true})

	inspect, err := c.ContainerInspect(ctx, cID)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(string(inspect.HostConfig.IpcMode), "private"))

	// restart the daemon with shareable default ipc mode
	d.Restart(t, "--iptables=false", "--ip6tables=false", "--default-ipc-mode=shareable")

	// check the container is still having private ipc mode
	inspect, err = c.ContainerInspect(ctx, cID)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(string(inspect.HostConfig.IpcMode), "private"))

	// check a new container is created with shareable ipc mode as per new daemon default
	cID = container.Run(ctx, t, c)
	defer c.ContainerRemove(ctx, cID, containertypes.RemoveOptions{Force: true})

	inspect, err = c.ContainerInspect(ctx, cID)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(string(inspect.HostConfig.IpcMode), "shareable"))
}

// TestDaemonHostGatewayIP verifies that when a magic string "host-gateway" is passed
// to ExtraHosts (--add-host) instead of an IP address, its value is set to
// 1. Daemon config flag value specified by host-gateway-ip or
// 2. IP of the default bridge network
// and is added to the /etc/hosts file
func TestDaemonHostGatewayIP(t *testing.T) {
	skip.If(t, testEnv.IsRemoteDaemon)
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")
	skip.If(t, testEnv.IsRootless, "rootless mode has different view of network")
	t.Parallel()

	ctx := testutil.StartSpan(baseContext, t)

	// Verify the IP in /etc/hosts is same as host-gateway-ip
	d := daemon.New(t)
	// Verify the IP in /etc/hosts is same as the default bridge's IP
	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false")
	c := d.NewClientT(t)
	cID := container.Run(ctx, t, c,
		container.WithExtraHost("host.docker.internal:host-gateway"),
	)
	res, err := container.Exec(ctx, c, cID, []string{"cat", "/etc/hosts"})
	assert.NilError(t, err)
	assert.Assert(t, is.Len(res.Stderr(), 0))
	assert.Equal(t, 0, res.ExitCode)
	inspect, err := c.NetworkInspect(ctx, "bridge", network.InspectOptions{})
	assert.NilError(t, err)
	assert.Check(t, is.Contains(res.Stdout(), inspect.IPAM.Config[0].Gateway))
	c.ContainerRemove(ctx, cID, containertypes.RemoveOptions{Force: true})
	d.Stop(t)

	// Verify the IP in /etc/hosts is same as host-gateway-ip
	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false", "--host-gateway-ip=6.7.8.9")
	cID = container.Run(ctx, t, c,
		container.WithExtraHost("host.docker.internal:host-gateway"),
	)
	res, err = container.Exec(ctx, c, cID, []string{"cat", "/etc/hosts"})
	assert.NilError(t, err)
	assert.Assert(t, is.Len(res.Stderr(), 0))
	assert.Equal(t, 0, res.ExitCode)
	assert.Check(t, is.Contains(res.Stdout(), "6.7.8.9"))
	c.ContainerRemove(ctx, cID, containertypes.RemoveOptions{Force: true})
	d.Stop(t)
}

// TestRestartDaemonWithRestartingContainer simulates a case where a container is in "restarting" state when
// dockerd is killed (due to machine reset or something else).
//
// Related to moby/moby#41817
//
// In this test we'll change the container state to "restarting".
// This means that the container will not be 'alive' when we attempt to restore in on daemon startup.
//
// We could do the same with `docker run -d --restart=always busybox:latest exit 1`, and then
// `kill -9` dockerd while the container is in "restarting" state. This is difficult to reproduce reliably
// in an automated test, so we manipulate on disk state instead.
func TestRestartDaemonWithRestartingContainer(t *testing.T) {
	skip.If(t, testEnv.IsRemoteDaemon, "cannot start daemon on remote test run")
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")

	t.Parallel()

	ctx := testutil.StartSpan(baseContext, t)

	d := daemon.New(t)
	defer d.Cleanup(t)

	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false")
	defer d.Stop(t)

	apiClient := d.NewClientT(t)

	// Just create the container, no need to start it to be started.
	// We really want to make sure there is no process running when docker starts back up.
	// We will manipulate the on disk state later
	id := container.Create(ctx, t, apiClient, container.WithRestartPolicy(containertypes.RestartPolicyAlways), container.WithCmd("/bin/sh", "-c", "exit 1"))

	d.Stop(t)

	d.TamperWithContainerConfig(t, id, func(c *realcontainer.Container) {
		c.SetRestarting(&realcontainer.ExitStatus{ExitCode: 1})
		c.HasBeenStartedBefore = true
	})

	d.Start(t, "--iptables=false", "--ip6tables=false")

	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	chOk, chErr := apiClient.ContainerWait(ctxTimeout, id, containertypes.WaitConditionNextExit)
	select {
	case <-chOk:
	case err := <-chErr:
		assert.NilError(t, err)
	}
}

// TestHardRestartWhenContainerIsRunning simulates a case where dockerd is
// killed while a container is running, and the container's task no longer
// exists when dockerd starts back up. This can happen if the system is
// hard-rebooted, for example.
//
// Regression test for moby/moby#45788
func TestHardRestartWhenContainerIsRunning(t *testing.T) {
	skip.If(t, testEnv.IsRemoteDaemon, "cannot start daemon on remote test run")
	skip.If(t, testEnv.DaemonInfo.OSType == "windows")

	t.Parallel()

	ctx := testutil.StartSpan(baseContext, t)

	d := daemon.New(t)
	defer d.Cleanup(t)

	d.StartWithBusybox(ctx, t, "--iptables=false", "--ip6tables=false")
	defer d.Stop(t)

	apiClient := d.NewClientT(t)

	// Just create the containers, no need to start them.
	// We really want to make sure there is no process running when docker starts back up.
	// We will manipulate the on disk state later.
	noPolicy := container.Create(ctx, t, apiClient, container.WithCmd("/bin/sh", "-c", "exit 1"))
	onFailure := container.Create(ctx, t, apiClient, container.WithRestartPolicy("on-failure"), container.WithCmd("/bin/sh", "-c", "sleep 60"))

	d.Stop(t)

	for _, id := range []string{noPolicy, onFailure} {
		d.TamperWithContainerConfig(t, id, func(c *realcontainer.Container) {
			c.SetRunning(nil, nil, time.Now())
			c.HasBeenStartedBefore = true
		})
	}

	d.Start(t, "--iptables=false", "--ip6tables=false")

	t.Run("RestartPolicy=none", func(t *testing.T) {
		ctx := testutil.StartSpan(ctx, t)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		inspect, err := apiClient.ContainerInspect(ctx, noPolicy)
		assert.NilError(t, err)
		assert.Check(t, is.Equal(inspect.State.Status, containertypes.StateExited))
		assert.Check(t, is.Equal(inspect.State.ExitCode, 255))
		finishedAt, err := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt)
		if assert.Check(t, err) {
			assert.Check(t, is.DeepEqual(finishedAt, time.Now(), opt.TimeWithThreshold(time.Minute)))
		}
	})

	t.Run("RestartPolicy=on-failure", func(t *testing.T) {
		ctx := testutil.StartSpan(ctx, t)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		inspect, err := apiClient.ContainerInspect(ctx, onFailure)
		assert.NilError(t, err)
		assert.Check(t, is.Equal(inspect.State.Status, containertypes.StateRunning))
		assert.Check(t, is.Equal(inspect.State.ExitCode, 0))
		finishedAt, err := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt)
		if assert.Check(t, err) {
			assert.Check(t, is.DeepEqual(finishedAt, time.Now(), opt.TimeWithThreshold(time.Minute)))
		}

		stopTimeout := 0
		assert.Assert(t, apiClient.ContainerStop(ctx, onFailure, containertypes.StopOptions{Timeout: &stopTimeout}))
	})
}

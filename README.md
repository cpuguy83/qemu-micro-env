## qemu-micro-env

qemu-micro-env was created to make it easy to test various projects against different kernel features.
For instance container runtimes need to be able to test against both cgroups v1 and cgroups v2, which can be
cumbersome to get running.
Its especially useful for fast a fast dev/test against these kernel features (e.g. after build caches are warmed a new VM should be ready in a couple of seconds).

qemu-micro-env utilizes buildkit to:
- Build a custom kernel (or use a pre-built kernel from a container image, or even your host machine)
- Build a custom rootfs to run in the VM (or use a pre-built rootfs from a container image, or even your host machine)
- Build a container image suitable for executing the VM (via qemu)

qemu-micro-env then executes a normal container in Docker with the built image and executes the VM in that container with qemu.
The exected VM, by default, is run as the same UID/GID as the user executing qemu-micro-env.
It is expected that docker is running locally to the client since it expects to get a docker.sock from the container (which is forwarded from the VM).

The executed VM runs a simple init process that:
- Sets up SSH access to the VM host (keys are passed through qemu over a fifo)
- Sets up basic networking
- Executes the provided command (default is to run dockerd)

The original goal of the project was to get dockerd running in the VM in a way
that is accessible outside the VM as quickly as possible. While many parts of
the project do try to let users do whatever they want, there are some
assumptions made about the original use case currently and may not work as expected right now.

## Usage

### Prerequisites

- Requires Docker 20.10+ (for buildkit)
- Ideally Docker 24.0+ with containerd as the storage backend (enables some optimizations in the build phase)

All the commands below elide the `--debug` flag, which is currently the only way to see what's being built.
Set that flag to see what's happening. This also adds some extra output during the kernel boot phase.

In the future this may be opened up to support custom buildkit daemons, but for
now it will only connect to the buildkit instance provided by dockerd.

### Basic

```console
$ go install github.com/cpuguy83/qemu-micro-env@latest
$ qemu-micro-env
```

With the above you will get a VM running with dockerd running in it.
The first execution may take a few minutes because it needs to build everything, subsequent runs should execute in a few seconds.

You can split the build and run phase as well:

```console
$ qemu-micro-env build | qemu-micro-env run
```

The `build` subcommand outputs a container image digest which the `run`
subcommand will read either from stdin or from the first argument to the command.

You can also tag an image with `-t` and then run it with `docker run`.

## Known issues

- Custom kernels give no output on boot and seem to exit unexpectedly (so as of right now only the default kernel works, though you can change things like cgroups v1 vs v2)
- Kernel modules get baked into the qcow image, so chagning kernel modules requires rebuilding that image (ideally this would be mounted from the host)
- Kernel image, config, and initrd are left out of the qcow since they are not neccessary for executing the VM, but this means processes in the VM can't access the kernel image and config as one might expect in a normal setup. (ideally these would be mounted from the host)
- Currently is using qemu userspace networking which is not ideal for performance, but is the easiest to get working and requires a proxy to make it work with docker port forwarding. (ideally this would be switched to use a tap device and a bridge)
- Output from the build phase would ideally be the same as `docker buildx build` (as an example) but right now it is not, and is only visible with `--debug` enabled.
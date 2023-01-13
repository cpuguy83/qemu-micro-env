ARG DISTRO=jammy

FROM ubuntu:22.04 AS rootfs-jammy
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y linux-image-kvm docker.io iptables ssh ntp

FROM golang:1.19 AS init
WORKDIR /opt/init
COPY cmd/init/go.* ./
RUN	\
	--mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg/mod \
	go mod download
COPY cmd/init/ ./
RUN	\
	--mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg/mod \
	CGO_ENABLED=0 go build -o /opt/init/init .

FROM rootfs-${DISTRO} AS resolvconf
RUN echo "nameserver 1.1.1.1" > /etc/resolv.conf.txt

FROM rootfs-${DISTRO} AS rootfs
RUN echo root:mayo | chpasswd
COPY --from=resolvconf /etc/resolv.conf.txt /etc/resolv.conf
COPY --from=init /opt/init/init /sbin/custom-init

FROM alpine:3.17 AS convert
RUN apk add --no-cache qemu-img e2fsprogs
WORKDIR /opt
RUN \
	--mount=from=rootfs,target=/opt/rootfs,source=/ \
	truncate -s 20G rootfs.img \
	&& mkfs.ext4 -d /opt/rootfs /opt/rootfs.img \
	&& qemu-img convert /opt/rootfs.img -O qcow2 /opt/rootfs.qcow2 \
	&& rm /opt/rootfs.img

FROM alpine:3.17 AS img
RUN apk add --no-cache qemu qemu-tools qemu-img qemu-system-x86_64 qemu-system-aarch64 bash openssh-client socat
WORKDIR /opt/qemu
COPY --link run.sh /opt/qemu/
EXPOSE 22
ENV FORWARD_SSH_PORT=22
ENTRYPOINT ["/opt/qemu/run.sh"]

FROM scratch
COPY --link --from=convert /opt/rootfs.qcow2 /
COPY --link --from=rootfs /boot/vmlinuz* /vmlinuz
COPY --link --from=rootfs /boot/initrd* /initrd.img

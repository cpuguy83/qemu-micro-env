#!/usr/bin/env bash

set -e

trap 'kill $(jobs -p)' EXIT

: "${VM_MEMORY:=4096}"
: "${VM_CPUS:=2}"

: "${DISTRO:=jammy}"
: "${DISTRO_DIR:=build/${DISTRO}}"

: "${FORWARD_SSH_PORT:=2222}"

KVM_ENABLED=0

: "${CGROUP_VERSION=2}"

check_vmx() {
    grep -E 'svm|vmx' /proc/cpuinfo >/dev/null
}

do_sudo() {
    if command -v sudo >/dev/null; then
        sudo "$@"
    else
        "$@"
    fi
}

if [ ! "${NO_KVM}" = "1" ] && [ check_vmx ] && [ -c "/dev/kvm" ]; then
    echo Enabling KVM
    KVM_OPTS="-enable-kvm -cpu host"
    MICROVM_OPTS=",x-option-roms=off,isa-serial=off,rtc=off"
    KVM_ENABLED=1
else
    echo KVM not available
fi

if [ -f "${DISTRO_DIR}/rootfs.qcow2" ]; then
    rootfs="${DISTRO_DIR}/rootfs.qcow2"
    rootfs_diff="${DISTRO_DIR}/rootfs-diff.qcow2"
else
    rootfs="rootfs.qcow2"
    rootfs_diff="rootfs-diff.qcow2"
fi

if [ -f "${DISTRO_DIR}/vmlinuz" ]; then
    kern="${DISTRO_DIR}/vmlinuz"
    initrd="${DISTRO_DIR}/initrd.img"
else
    if [ -f "vmlinuz" ]; then
        kern=vmlinuz
        initrd=initrd.img
    else
        echo "Falling back to host kernel" >&2
        kern=/boot/vmlinuz
        initrd=/boot/initrd.img
    fi
fi

rootfs_diff="${DISTRO_DIR}/rootfs-diff.qcow2"
qemu-img create -f qcow2 -b rootfs.qcow2 -F qcow2 ${rootfs_diff} >/dev/null

if [ "${KVM_ENABLED}" = "1" ]; then
    withSudo="do_sudo"
fi

if [ ! -f "${DISTRO_DIR}/ssh_key" ]; then
    ssh-keygen -t ed25519 -f "${DISTRO_DIR}/ssh_key" -N ""
fi

(
    set +e
    rm ${DISTRO_DIR}/docker.sock 2>/dev/null
    known_hosts="${DISTRO_DIR}/known_hosts"
    rm -f "${known_hosts}"

    while true; do
        # TODO: strict hostkey checking
        ssh -f -oBatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile="${known_hosts}" -o ExitOnForwardFailure=yes -i "${DISTRO_DIR}/ssh_key" -nNT -L "$(pwd)/${DISTRO_DIR}/docker.sock":/var/run/docker.sock root@127.0.0.1 -p ${FORWARD_SSH_PORT}
        if [ "$?" = "0" ]; then
            echo socket ready
            break
        fi

        ${withSudo} socat UNIX-CONNECT:"${DISTRO_DIR}/authorized_keys" - <${DISTRO_DIR}/ssh_key.pub
        sleep 1
    done
) &

${withSudo} qemu-system-x86_64 \
    -M microvm${MICROVM_OPTS} \
    -m ${VM_MEMORY} \
    -smp ${VM_CPUS} \
    -no-reboot \
    ${KVM_OPTS} \
    -no-acpi \
    -nodefaults \
    -no-user-config \
    -nographic \
    -device virtio-serial-device -chardev stdio,id=virtiocon0 -device virtconsole,chardev=virtiocon0 \
    -drive "id=root,file=${rootfs_diff},format=qcow2,if=none" -device virtio-blk-device,drive=root \
    -device virtio-rng-device \
    -kernel ${kern} \
    -initrd ${initrd} \
    -append "console=hvc0 root=/dev/vda rw acpi=off reboot=t panic=-1 init=/sbin/custom-init - --cgroup-version ${CGROUP_VERSION}" \
    -netdev user,id=mynet0,hostfwd=tcp::${FORWARD_SSH_PORT}-:22,hostfwd=tcp::6443-:6443,net=192.168.76.0/24,dhcpstart=192.168.76.9 -device virtio-net-device,netdev=mynet0 \
    -chardev socket,server=on,wait=off,id=docker,path=${DISTRO_DIR}/authorized_keys \
    -device virtio-serial-device \
    -device virtserialport,chardev=docker,name=authorized_keys

kill $(jobs -p)

DOCKER ?= docker
BUILD ?= $(DOCKER) buildx build
OUTDIR ?= build

export DISTRO ?= jammy

KVM_DOCKER_FLAGS ?= $(if $(shell [ -c /dev/kvm ] && echo 1),--device /dev/kvm)

run: $(OUTDIR)/img $(OUTDIR)/$(DISTRO)/vmlinuz
	$(DOCKER) run \
		${KVM_DOCKER_FLAGS} \
		--mount=type=bind,source=$(PWD)/$(OUTDIR)/$(DISTRO),target=/opt/qemu/distro \
		-e DISTRO_DIR=distro \
		-e NO_KVM \
		-e CGROUP_VERSION \
		-e VM_CPUS \
		-w /opt/qemu \
		--rm \
		-P \
		-it \
		$(shell cat $<)

.PHONY: build
$(OUTDIR)/img: Dockerfile run.sh
	mkdir -p $(@D)
	$(BUILD) --target=img --iidfile=$(@) --load .

$(OUTDIR)/$(DISTRO)/%: Dockerfile
	mkdir -p $(@D)
	$(BUILD) --build-arg DISTRO --progress=plain --output=$(@D) .

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

REGISTRY ?= quay.io
REPOSITORY ?= nirsof
NAME ?= cirros
VERSION ?= 0.6.2

work := work.qcow2
disk := cirros-$(VERSION).qcow2
container := $(REGISTRY)/$(REPOSITORY)/$(NAME):$(VERSION)

all: $(disk)
	podman build --tag $(container) --build-arg disk=$(disk) .

push:
	podman push $(container)

$(disk):
	./download-cirros --cirros-version $(VERSION) --output $(work)
	virt-customize --add $(work) \
		--copy-in ../ramen:/tmp \
		--run-command "/tmp/ramen/install" \
		--delete "/tmp/ramen"
	qemu-img convert -f qcow2 -O qcow2 -c $(work) $@

vm: $(disk) nocloud.iso
	# Create a test overlay image top of the cirros disk.
	test -f test.qcow2 || qemu-img create -f qcow2 -b $(disk) -F qcow2 test.qcow2
	# Start a vm using the disk and nocloud.iso to avoid metadata server lookup.
	qemu-kvm -m 256 \
		-net nic \
		-net user \
		-drive file=test.qcow2,format=qcow2,if=virtio \
		-drive file=nocloud.iso,format=raw,if=virtio

nocloud.iso:
	cloud-localds $@ user-data meta-data

clean:
	podman image rm -f $(container)
	rm -f $(disk)
	rm -f $(work)
	rm -f nocloud.iso
	rm -f test.qcow2

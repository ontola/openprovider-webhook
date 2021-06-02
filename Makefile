OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)

IMAGE_NAME := "ontola/openprovider-webhook"
IMAGE_TAG := "0.1.0"

OUT := $(shell pwd)/manifests

KUBEBUILDER_VERSION=2.3.2

$(shell mkdir -p "$(OUT)")

test: _test/kubebuilder
	go test -v .

_test/kubebuilder:
	curl -fsSL https://github.com/kubernetes-sigs/kubebuilder/releases/download/v$(KUBEBUILDER_VERSION)/kubebuilder_$(KUBEBUILDER_VERSION)_$(OS)_$(ARCH).tar.gz -o kubebuilder-tools.tar.gz
	mkdir -p _test/kubebuilder
	tar -xvf kubebuilder-tools.tar.gz
	mv kubebuilder_$(KUBEBUILDER_VERSION)_$(OS)_$(ARCH)/bin _test/kubebuilder/
	rm kubebuilder-tools.tar.gz
	rm -R kubebuilder_$(KUBEBUILDER_VERSION)_$(OS)_$(ARCH)

clean: clean-kubebuilder

clean-kubebuilder:
	rm -Rf _test/kubebuilder

build:
	docker build -t "$(IMAGE_NAME):$(IMAGE_TAG)" .

render:
	helm template \
	    --name-template openprovider-webhook \
        --set image.repository=$(IMAGE_NAME) \
        --set image.tag=$(IMAGE_TAG) \
        deploy/openprovider-webhook > "$(OUT)/openprovider-webhook.$(IMAGE_TAG).yaml"

.PHONY: build test fmt vet generate manifests install uninstall

CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.3
CONTROLLER_IMAGE ?= ghcr.io/lannonbr/node-schedule:latest
NAMESPACE ?= automation

build:
	go build ./...

test:
	go test ./...

fmt:
	gofmt -w $$(find api cmd internal -name '*.go')

vet:
	go vet ./...

generate:
	$(CONTROLLER_GEN) object:headerFile="" paths="./..."

manifests:
	$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=config/crds

install:
	helm upgrade --install nodeschedule ./config --namespace "$(NAMESPACE)" --create-namespace --set-string controllerImage="$(CONTROLLER_IMAGE)" --wait

uninstall:
	helm uninstall nodeschedule --namespace "$(NAMESPACE)" --ignore-not-found

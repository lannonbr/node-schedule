.PHONY: build test fmt vet generate manifests install uninstall

CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.3
CONTROLLER_IMAGE ?= ghcr.io/lannonbr/node-schedule:latest

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
	$(CONTROLLER_GEN) crd paths="./..." output:crd:artifacts:config=config/crd/bases

install:
	kubectl apply -f config/default/namespace.yaml
	kubectl apply -f config/crd/bases/automation.lannonbr.com_nodeschedules.yaml
	kubectl wait --for=condition=Established crd/nodeschedules.automation.lannonbr.com --timeout=60s
	kubectl apply -f config/rbac
	kubectl apply -f config/manager/manager.yaml
	kubectl -n automation set image deployment/nodeschedule-controller manager=$(CONTROLLER_IMAGE)

uninstall:
	kubectl delete -f config/manager/manager.yaml --ignore-not-found
	kubectl delete -f config/rbac --ignore-not-found
	kubectl delete -f config/crd/bases/automation.lannonbr.com_nodeschedules.yaml --ignore-not-found
	kubectl delete -f config/default/namespace.yaml --ignore-not-found

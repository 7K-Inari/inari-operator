IMG ?= ghcr.io/7k-inari/inari-operator:latest
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5
SETUP_ENVTEST ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.20
ENVTEST_K8S_VERSION ?= 1.32.0

.PHONY: build
build:
	go build ./...

.PHONY: test
test: envtest
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use -p path $(ENVTEST_K8S_VERSION))" go test ./... -coverprofile=cover.out

.PHONY: envtest
envtest:
	@$(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) >/dev/null

.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: generate
generate:
	$(CONTROLLER_GEN) object paths=./api/...

.PHONY: manifests
manifests:
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:dir=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths=./... output:rbac:dir=config/rbac

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: deploy
deploy:
	kustomize build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy:
	kustomize build config/default | kubectl delete -f - || true

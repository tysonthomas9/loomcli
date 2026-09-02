# Pipeline for bringing up a disposable Loom + fleet-db stack on a GCP VM.
#
#   make up          build + push images, apply, wait until healthy (set TUNNEL_PORT_BASE)
#   make smoke       prove the stack end to end (exits non-zero on failure)
#   make tunnel      open IAP tunnels and print the browser URL (set TUNNEL_PORT_BASE)
#   make down        destroy everything this stack created
#
# Every stack is named, so several can run at once without colliding:
#   make up NAME=loom-pr512 TUNNEL_PORT_BASE=18100
#
SHELL := /bin/bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c

# Every recipe below is one shell per target (.ONESHELL). GNU Make 3.81 -- what
# macOS ships as /usr/bin/make -- silently ignores the directive and runs each
# line in its own shell, so the image-build lock loop spins forever on
# `mkdir ""`. Fail at parse time instead.
ifeq ($(filter oneshell,$(.FEATURES)),)
$(error GNU Make 3.82+ is required (.ONESHELL); this is $(MAKE_VERSION). macOS: brew install make, then run gmake)
endif

REPO_ROOT := $(shell git rev-parse --show-toplevel)
NAME      ?= loom-stack
PROJECT   ?= $(shell gcloud config get-value project 2>/dev/null)
REGION    ?= us-central1
ZONE      ?= $(REGION)-a
REGISTRY  ?= $(REGION)-docker.pkg.dev/$(PROJECT)/loom
FLEETDB_SRC ?= $(REPO_ROOT)/../fleet-db
CONTAINER_CLI ?= docker
CONTAINER_ARGS ?=
# The VM boots ubuntu-2404-lts-amd64 (vm.tf). Without an explicit platform an
# Apple Silicon host pushes arm64 images and no container on the VM starts.
# Cross-building uses the binfmt emulators Docker Desktop and Podman machine
# both ship.
PLATFORM ?= linux/amd64
REGISTRY_HOST = $(word 1,$(subst /, ,$(REGISTRY)))
REGISTRY_LOCATION = $(patsubst %-docker.pkg.dev,%,$(REGISTRY_HOST))
REGISTRY_PROJECT = $(word 2,$(subst /, ,$(REGISTRY)))
REGISTRY_REPOSITORY = $(word 3,$(subst /, ,$(REGISTRY)))
TF_STATE_BUCKET ?= $(PROJECT)-loom-tfstate
IMAGE_BUILD_LOCK ?= $(REPO_ROOT)/.loom-gcp-image-build.lock

# Tag by commit so a stack always names the code it is running -- but a commit
# id names a COMMIT, not a working tree. Building from a dirty checkout pushed
# modified source under the clean SHA, so var.loom_image never changed, the VM
# was never replaced (nothing in its boot config differed), and wait-healthy
# reported success after probing the OLD container. Silent, and it looked like
# the change simply had no effect.
#
# So: append a hash of the actual delta -- `git diff HEAD` for tracked edits,
# plus the hashed CONTENT of untracked files, since those land in the build
# context too and listing their names alone would not notice an edit. The UI
# build is a special case: its generated `dist/` is ignored by git but is
# copied into the UI image, so its file contents are hashed separately after
# the frontend prerequisite has completed.
# `cd ... || exit 0` and not `cd ... &&`: with && a missing checkout sends the
# whole thing into the || branch, which would then hash whatever repo make was
# invoked from and stamp THAT delta onto fleet-db's tag.
tree_delta = $(shell ( cd $(1) 2>/dev/null || exit 0; git diff --quiet HEAD 2>/dev/null && test -z "$$(git ls-files -o --exclude-standard)" || printf -- '-dirty.%s' "$$( { git diff HEAD; git ls-files -o --exclude-standard -z | xargs -0 git hash-object; } 2>/dev/null | shasum | cut -c1-8 )" ) 2>/dev/null)

LOOM_DELTA    = $(call tree_delta,$(REPO_ROOT))
FLEETDB_DELTA := $(call tree_delta,$(FLEETDB_SRC))

ui_context_hash = $(shell ( cd $(1) 2>/dev/null || exit 0; if [ -d internal/webui/frontend/dist ]; then find internal/webui/frontend/dist -type f -print0 | xargs -0 shasum 2>/dev/null | LC_ALL=C sort | shasum | cut -c1-8; else printf missing; fi ) 2>/dev/null)
UI_HASH      = $(call ui_context_hash,$(REPO_ROOT))

TAG       ?= $(shell git -C $(REPO_ROOT) rev-parse --short HEAD)-ui.$(UI_HASH)$(LOOM_DELTA)$(TAG_SUFFIX)
# Tagged from ITS OWN commit. Tagging fleet-db with loom's SHA meant a
# fleet-db change did not alter the tag, so the stack kept the stale image.
FLEETDB_TAG ?= $(shell git -C $(FLEETDB_SRC) rev-parse --short HEAD 2>/dev/null || echo unknown)$(FLEETDB_DELTA)

# CODEX=1 builds a loom image WITH the codex CLI and points the stack at an
# existing Secret Manager secret holding auth.json. The binary is a build arg
# (test/local-mode/Dockerfile defaults INSTALL_CODEX=false), so an image built
# without it makes the entrypoint fail with "codex binary is not installed".
CODEX        ?= 0
CODEX_SECRET ?= loom-codex-auth
CODEX_CLI_VERSION ?= 0.144.1
TUNNEL_PORT_BASE ?=
ifeq ($(CODEX),1)
  LOOM_BUILD_ARGS = --build-arg INSTALL_CODEX=true --build-arg CODEX_CLI_VERSION=$(CODEX_CLI_VERSION)
  # read_only on the plan role selects codex's `--sandbox read-only`, which a
  # planner cannot work under: it needs the Loom API, and that sandbox unshares
  # the network. Relax it. See variables.tf for the full story.
  CODEX_TFVARS    = -var codex_auth_secret=$(CODEX_SECRET) -var plan_role_read_only=false
  TAG_SUFFIX      = -codex
else
  LOOM_BUILD_ARGS =
  CODEX_TFVARS    =
  TAG_SUFFIX      =
endif

TUNNEL_TFVARS = $(if $(TUNNEL_PORT_BASE),-var tunnel_port_base=$(TUNNEL_PORT_BASE),)

# Works with either binary. OpenTofu is the default because Terraform's BUSL
# licence makes it awkward in some orgs; override with TF_BIN=terraform.
TF_BIN ?= $(shell command -v tofu 2>/dev/null || command -v terraform 2>/dev/null)
# Terraform's selected workspace is stored in .terraform/environment. A
# shared environment file makes concurrent `make up NAME=a` / `NAME=b` calls
# race between workspace select and apply. Give every NAME its own data dir;
# the backend lock still serialises two operations on the same stack.
TF = TF_DATA_DIR="$(CURDIR)/.terraform-data/$(NAME)" $(TF_BIN) -chdir="$(CURDIR)"

TFVARS = -var project_id=$(PROJECT) -var region=$(REGION) -var zone=$(ZONE) -var name=$(NAME) \
         -var registry_host=$(REGISTRY_HOST) -var registry_project=$(REGISTRY_PROJECT) \
         -var registry_location=$(REGISTRY_LOCATION) -var registry_repository=$(REGISTRY_REPOSITORY) \
         $(CODEX_TFVARS) $(TUNNEL_TFVARS)

# Tags are convenient build/push labels, but they are mutable registry names.
# Resolve them only after the images prerequisite has completed and pass the
# resulting digests to Terraform so a rebuild can never leave the old VM
# running just because a tag string stayed the same.
FLEETDB_DIGEST = $(shell gcloud artifacts docker images describe "$(REGISTRY)/fleet-db:$(FLEETDB_TAG)" --project="$(REGISTRY_PROJECT)" --format='value(image_summary.digest)' 2>/dev/null)
LOOM_DIGEST    = $(shell gcloud artifacts docker images describe "$(REGISTRY)/loom:$(TAG)" --project="$(REGISTRY_PROJECT)" --format='value(image_summary.digest)' 2>/dev/null)
UI_DIGEST      = $(shell gcloud artifacts docker images describe "$(REGISTRY)/loom-ui:$(TAG)" --project="$(REGISTRY_PROJECT)" --format='value(image_summary.digest)' 2>/dev/null)

.PHONY: help inputs preflight backend frontend frontend-unlocked print-tags state images apply up smoke tunnel status down fmt validate

help:
	@grep -E '^[a-z-]+:.*?##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | column -t -s "$$(printf '\t')"

preflight: ## Fail early on the things that otherwise fail slowly
	@test -n "$(TF_BIN)" || { echo "missing: tofu or terraform"; exit 1; }
	@for b in gcloud $(CONTAINER_CLI) git; do command -v $$b >/dev/null || { echo "missing: $$b"; exit 1; }; done
	@test -n "$(PROJECT)" || { echo "PROJECT unset and no gcloud default project"; exit 1; }
	@# The gcloud default project is often stale or belongs to another org.
	@# Checking it here turns a confusing IAM failure mid-apply into one line.
	@gcloud projects describe "$(PROJECT)" >/dev/null 2>&1 || { \
		echo "cannot access project '$(PROJECT)' as $$(gcloud config get-value account 2>/dev/null)"; \
		echo "pass PROJECT=<id> or run: gcloud config set project <id>"; exit 1; }
	@test -d "$(FLEETDB_SRC)" || { echo "fleet-db checkout not found at $(FLEETDB_SRC); set FLEETDB_SRC"; exit 1; }
	@test -f "$(FLEETDB_SRC)/internal/api/workspace_files.go" && \
		test -f "$(FLEETDB_SRC)/internal/storage/blob/s3_store.go" && \
		grep -q "RegisterWorkspaceFileRoutes" "$(FLEETDB_SRC)/cmd/fleet-db/main.go" || { \
			echo "FLEETDB_SRC does not include the wired workspace-file API and S3 store; use a workspace-file-api FleetDB checkout" >&2; \
			exit 1; }
	@if [ "$(REGISTRY_PROJECT)" = "$(PROJECT)" ] && [ -n "$(REGISTRY_REPOSITORY)" ]; then \
		if ! gcloud artifacts repositories describe "$(REGISTRY_REPOSITORY)" --location="$(REGISTRY_LOCATION)" --project="$(REGISTRY_PROJECT)" >/dev/null 2>&1; then \
			gcloud artifacts repositories create "$(REGISTRY_REPOSITORY)" --repository-format=docker --location="$(REGISTRY_LOCATION)" --project="$(REGISTRY_PROJECT)" >/dev/null 2>&1 || \
			gcloud artifacts repositories describe "$(REGISTRY_REPOSITORY)" --location="$(REGISTRY_LOCATION)" --project="$(REGISTRY_PROJECT)" >/dev/null 2>&1 || exit 1; \
		fi; \
	fi
	@if [ "$(CONTAINER_CLI)" = podman ]; then \
		gcloud auth print-access-token | $(CONTAINER_CLI) $(CONTAINER_ARGS) login --username oauth2accesstoken --password-stdin "$(REGISTRY_HOST)" >/dev/null; \
	else \
		gcloud auth configure-docker $(REGISTRY_HOST) --quiet; \
	fi
ifeq ($(CODEX),1)
	@gcloud secrets describe $(CODEX_SECRET) --project=$(PROJECT) >/dev/null 2>&1 || { \
		echo "CODEX=1 but secret '$(CODEX_SECRET)' does not exist. Create it (it holds your"; \
		echo "personal credential, so Terraform deliberately does not):"; \
		echo "  gcloud secrets create $(CODEX_SECRET) --project=$(PROJECT) \\"; \
		echo "    --replication-policy=automatic --data-file=\"\$$HOME/.codex/auth.json\""; exit 1; }
endif

inputs:
	@test -n "$(TUNNEL_PORT_BASE)" || { echo "TUNNEL_PORT_BASE is required; choose a unique local UI port for this stack" >&2; exit 1; }

backend: ## Ensure the shared encrypted Terraform state bucket exists
	@test -n "$(PROJECT)" || { echo "PROJECT is required to create the Terraform state bucket" >&2; exit 1; }
	@if ! gcloud storage buckets describe "gs://$(TF_STATE_BUCKET)" --project="$(PROJECT)" >/dev/null 2>&1; then \
		gcloud storage buckets create "gs://$(TF_STATE_BUCKET)" --location="$(REGION)" --uniform-bucket-level-access --project="$(PROJECT)" >/dev/null 2>&1 || \
		gcloud storage buckets describe "gs://$(TF_STATE_BUCKET)" --project="$(PROJECT)" >/dev/null 2>&1 || exit 1; \
	fi
	@gcloud storage buckets update "gs://$(TF_STATE_BUCKET)" --versioning --project="$(PROJECT)" >/dev/null

frontend: preflight ## Build the SPA bundle used by the UI image
	lock="$(IMAGE_BUILD_LOCK)"
	while ! mkdir "$$lock" 2>/dev/null; do sleep 1; done
	trap 'rmdir "$$lock"' EXIT
	$(MAKE) --no-print-directory frontend-unlocked

frontend-unlocked:
	cd $(REPO_ROOT) && make build-frontend

print-tags:
	@printf '%s\n' "$(FLEETDB_TAG)" "$(TAG)"

images: preflight ## Build and push fleet-db, loom, and the SPA image (CODEX=1 adds the codex CLI)
	lock="$(IMAGE_BUILD_LOCK)"
	while ! mkdir "$$lock" 2>/dev/null; do sleep 1; done
	trap 'rmdir "$$lock"' EXIT
	$(MAKE) --no-print-directory frontend-unlocked
	tags=( $$($(MAKE) --no-print-directory print-tags) )
	fleetdb_image="$(REGISTRY)/fleet-db:$${tags[0]}"
	loom_image="$(REGISTRY)/loom:$${tags[1]}"
	ui_image="$(REGISTRY)/loom-ui:$${tags[1]}"
	$(CONTAINER_CLI) $(CONTAINER_ARGS) build --platform $(PLATFORM) -t "$$fleetdb_image" -f $(FLEETDB_SRC)/deploy/docker/Dockerfile $(FLEETDB_SRC)
	$(CONTAINER_CLI) $(CONTAINER_ARGS) build --platform $(PLATFORM) $(LOOM_BUILD_ARGS) -t "$$loom_image" -f $(REPO_ROOT)/test/local-mode/Dockerfile $(REPO_ROOT)
	@# Context is a tar of exactly the two paths the image needs, streamed on
	@# stdin. Building from the repo root instead makes the root .dockerignore
	@# apply, and it excludes internal/webui/frontend/dist/ -- the very bundle
	@# this image exists to serve -- so the COPY fails however recently you ran
	@# build-frontend. A stdin tar carries no ignore file, so nothing is dropped.
	tar -C $(REPO_ROOT) -cf - internal/webui/frontend/dist deploy/terraform/ui \
	  | $(CONTAINER_CLI) $(CONTAINER_ARGS) build --platform $(PLATFORM) -t "$$ui_image" -f deploy/terraform/ui/Dockerfile -
	$(CONTAINER_CLI) $(CONTAINER_ARGS) push "$$fleetdb_image"
	$(CONTAINER_CLI) $(CONTAINER_ARGS) push "$$loom_image"
	$(CONTAINER_CLI) $(CONTAINER_ARGS) push "$$ui_image"

# NAME selects a Terraform WORKSPACE, not just resource names. Without this
# every stack shares one state file in this directory: applying NAME=b after
# NAME=a makes Terraform read b as edits to a, and `make down NAME=b` destroys
# stack a. State lives in terraform.tfstate.d/<name>/; the workspace selector
# itself lives in the per-name `.terraform-data/` directory.
#
# One caveat this does not fix: the local backend keeps a single lock, so two
# applies from this directory at once still serialise.
state: backend ## Select this stack's Terraform workspace (creates it on first use)
	@[[ "$(NAME)" =~ ^[a-z][-a-z0-9]{4,28}[a-z0-9]$$ ]] || { echo "NAME must be 6-30 lowercase characters, start with a letter, and end alphanumeric" >&2; exit 1; }
	@$(TF) init -input=false -backend-config="bucket=$(TF_STATE_BUCKET)" -backend-config="prefix=loom-gcp" >/dev/null
	@$(TF) workspace select -or-create "$(NAME)" >/dev/null
	@echo "state: workspace $$($(TF) workspace show)"

apply: inputs state ## Apply infrastructure only (assumes images already pushed)
	test -n "$(FLEETDB_DIGEST)" && test -n "$(LOOM_DIGEST)" && test -n "$(UI_DIGEST)" || { echo "published image digest missing; run make images first" >&2; exit 1; }
	@echo "if apply fails, inspect or clean up with: make down NAME=$(NAME) PROJECT=$(PROJECT) REGION=$(REGION) ZONE=$(ZONE) TF_STATE_BUCKET=$(TF_STATE_BUCKET)" >&2
	$(TF) apply -auto-approve -input=false $(TFVARS) \
		-var fleetdb_image="$(REGISTRY)/fleet-db@$(FLEETDB_DIGEST)" \
		-var loom_image="$(REGISTRY)/loom@$(LOOM_DIGEST)" \
		-var ui_image="$(REGISTRY)/loom-ui@$(UI_DIGEST)"

up: inputs images apply ## Full pipeline: images -> infra -> wait healthy
	@echo "waiting for the stack to report healthy..."
	@echo "if readiness fails, inspect or clean up with: make down NAME=$(NAME) PROJECT=$(PROJECT) REGION=$(REGION) ZONE=$(ZONE) TF_STATE_BUCKET=$(TF_STATE_BUCKET)" >&2
	$(CURDIR)/scripts/wait-healthy $(NAME) $(PROJECT) $(ZONE) \
		$$($(TF) output -raw api_port) \
		$$($(TF) output -raw ui_port)
	@echo
	@$(TF) output -raw iap_tunnel_ui
	@echo

# Every expectation comes from `tofu output`, i.e. from the stack as deployed.
# Reconstructing them from Make arguments meant `make up CODEX=1` followed by
# the documented `make smoke NAME=...` asserted read_only=true against a stack
# deployed read_only=false -- a healthy stack failing its own gate because the
# second command was missing a flag the first one had.
smoke: state ## End-to-end proof: workspace file round trip through GCS
	@$(CURDIR)/scripts/smoke $(NAME) $(PROJECT) $(ZONE) \
		$$($(TF) output -raw loom_workspace) \
		$$($(TF) output -raw plan_role_read_only) \
		$$($(TF) output -raw fleetdb_port) \
		$$($(TF) output -raw api_port) \
		$$($(TF) output -raw ui_port)

tunnel: state ## Open IAP tunnels in the background and print the URL
	@test -n "$(TUNNEL_PORT_BASE)" || { echo "TUNNEL_PORT_BASE is required for a collision-free local tunnel (reuse the value passed to make up)" >&2; exit 1; }
	actual_tunnel_port="$$($(TF) output -raw tunnel_ui_port)"
	@test "$$actual_tunnel_port" = "$(TUNNEL_PORT_BASE)" || { echo "TUNNEL_PORT_BASE=$(TUNNEL_PORT_BASE) does not match deployed tunnel_ui_port=$$actual_tunnel_port; reapply with the same base" >&2; exit 1; }
	@$(CURDIR)/scripts/tunnel \
		--name="$(NAME)" --project="$(PROJECT)" --zone="$(ZONE)" \
		--ui-local="$$($(TF) output -raw tunnel_ui_port)" \
		--api-local="$$($(TF) output -raw tunnel_api_port)" \
		--db-local="$$($(TF) output -raw tunnel_fleetdb_port)" \
		--ui-remote="$$($(TF) output -raw ui_port)" \
		--api-remote="$$($(TF) output -raw api_port)" \
		--db-remote="$$($(TF) output -raw fleetdb_port)" \
		--workspace="$$($(TF) output -raw loom_workspace)"

status: ## Container state on the VM
	gcloud compute ssh $(NAME) --project $(PROJECT) --zone $(ZONE) --tunnel-through-iap --quiet \
		--command 'sudo docker compose -f /opt/loom/docker-compose.yml ps; systemctl is-active loom-stack'

down: state ## Destroy this stack (bucket, HMAC key, secrets, VM, firewall)
	tunnel_port_base="$$($(TF) output -raw tunnel_ui_port)"
	$(TF) destroy -auto-approve -input=false $(TFVARS) \
		-var tunnel_port_base="$$tunnel_port_base" \
		-var fleetdb_image=destroy-placeholder \
		-var loom_image=destroy-placeholder \
		-var ui_image=destroy-placeholder

fmt:
	$(TF) fmt -recursive

validate:
	$(TF) init -backend=false -input=false >/dev/null
	$(TF) validate

# Pipeline for bringing up a disposable Loom + fleet-db stack on a GCP VM.
#
#   make up          build + push images, apply, wait until healthy
#   make smoke       prove the stack end to end (exits non-zero on failure)
#   make tunnel      open IAP tunnels and print the browser URL
#   make down        destroy everything this stack created
#
# Every stack is named, so several can run at once without colliding:
#   make up NAME=loom-pr512
#
SHELL := /bin/bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c

REPO_ROOT := $(shell git rev-parse --show-toplevel)
NAME      ?= loom-stack
PROJECT   ?= $(shell gcloud config get-value project 2>/dev/null)
REGION    ?= us-central1
ZONE      ?= $(REGION)-a
REGISTRY  ?= $(REGION)-docker.pkg.dev/$(PROJECT)/loom
FLEETDB_SRC ?= $(REPO_ROOT)/../fleet-db

# Tag by commit so a stack always names the code it is running -- but a commit
# id names a COMMIT, not a working tree. Building from a dirty checkout pushed
# modified source under the clean SHA, so var.loom_image never changed, the VM
# was never replaced (nothing in its boot config differed), and wait-healthy
# reported success after probing the OLD container. Silent, and it looked like
# the change simply had no effect.
#
# So: append a hash of the actual delta -- `git diff HEAD` for tracked edits,
# plus the hashed CONTENT of untracked files, since those land in the build
# context too and listing their names alone would not notice an edit. Clean
# tree -> empty suffix, so committed builds keep their plain <sha> tags.
# `cd ... || exit 0` and not `cd ... &&`: with && a missing checkout sends the
# whole thing into the || branch, which would then hash whatever repo make was
# invoked from and stamp THAT delta onto fleet-db's tag.
tree_delta = $(shell ( cd $(1) 2>/dev/null || exit 0; git diff --quiet HEAD 2>/dev/null && test -z "$$(git ls-files -o --exclude-standard)" || printf -- '-dirty.%s' "$$( { git diff HEAD; git ls-files -o --exclude-standard -z | xargs -0 git hash-object; } 2>/dev/null | shasum | cut -c1-8 )" ) 2>/dev/null)

LOOM_DELTA    := $(call tree_delta,$(REPO_ROOT))
FLEETDB_DELTA := $(call tree_delta,$(FLEETDB_SRC))

TAG       ?= $(shell git -C $(REPO_ROOT) rev-parse --short HEAD)$(LOOM_DELTA)$(TAG_SUFFIX)
# Tagged from ITS OWN commit. Tagging fleet-db with loom's SHA meant a
# fleet-db change did not alter the tag, so the stack kept the stale image.
FLEETDB_TAG ?= $(shell git -C $(FLEETDB_SRC) rev-parse --short HEAD 2>/dev/null || echo unknown)$(FLEETDB_DELTA)

# CODEX=1 builds a loom image WITH the codex CLI and points the stack at an
# existing Secret Manager secret holding auth.json. The binary is a build arg
# (test/local-mode/Dockerfile defaults INSTALL_CODEX=false), so an image built
# without it makes the entrypoint fail with "codex binary is not installed".
CODEX        ?= 0
CODEX_SECRET ?= loom-codex-auth
ifeq ($(CODEX),1)
  LOOM_BUILD_ARGS = --build-arg INSTALL_CODEX=true
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

# Works with either binary. OpenTofu is the default because Terraform's BUSL
# licence makes it awkward in some orgs; override with TF_BIN=terraform.
TF_BIN ?= $(shell command -v tofu 2>/dev/null || command -v terraform 2>/dev/null)
TF = $(TF_BIN) -chdir=$(CURDIR)

TFVARS = -var project_id=$(PROJECT) -var region=$(REGION) -var zone=$(ZONE) -var name=$(NAME) \
         -var fleetdb_image=$(REGISTRY)/fleet-db:$(FLEETDB_TAG) \
         -var loom_image=$(REGISTRY)/loom:$(TAG) \
         -var ui_image=$(REGISTRY)/loom-ui:$(TAG) $(CODEX_TFVARS)

.PHONY: help preflight state images apply up smoke tunnel status down fmt validate

help:
	@grep -E '^[a-z-]+:.*?##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | column -t -s "$$(printf '\t')"

preflight: ## Fail early on the things that otherwise fail slowly
	@test -n "$(TF_BIN)" || { echo "missing: tofu or terraform"; exit 1; }
	@for b in gcloud docker git; do command -v $$b >/dev/null || { echo "missing: $$b"; exit 1; }; done
	@test -n "$(PROJECT)" || { echo "PROJECT unset and no gcloud default project"; exit 1; }
	@# The gcloud default project is often stale or belongs to another org.
	@# Checking it here turns a confusing IAM failure mid-apply into one line.
	@gcloud projects describe "$(PROJECT)" >/dev/null 2>&1 || { \
		echo "cannot access project '$(PROJECT)' as $$(gcloud config get-value account 2>/dev/null)"; \
		echo "pass PROJECT=<id> or run: gcloud config set project <id>"; exit 1; }
	@test -d "$(FLEETDB_SRC)" || { echo "fleet-db checkout not found at $(FLEETDB_SRC); set FLEETDB_SRC"; exit 1; }
	@gcloud artifacts repositories describe loom --location=$(REGION) --project=$(PROJECT) >/dev/null 2>&1 || \
		gcloud artifacts repositories create loom --repository-format=docker --location=$(REGION) --project=$(PROJECT)
	@gcloud auth configure-docker $(REGION)-docker.pkg.dev --quiet
ifeq ($(CODEX),1)
	@gcloud secrets describe $(CODEX_SECRET) --project=$(PROJECT) >/dev/null 2>&1 || { \
		echo "CODEX=1 but secret '$(CODEX_SECRET)' does not exist. Create it (it holds your"; \
		echo "personal credential, so Terraform deliberately does not):"; \
		echo "  gcloud secrets create $(CODEX_SECRET) --project=$(PROJECT) \\"; \
		echo "    --replication-policy=automatic --data-file=\"\$$HOME/.codex/auth.json\""; exit 1; }
endif

images: preflight ## Build and push fleet-db, loom, and the SPA image (CODEX=1 adds the codex CLI)
	cd $(REPO_ROOT) && make build-frontend
	docker build -t $(REGISTRY)/fleet-db:$(FLEETDB_TAG) -f $(FLEETDB_SRC)/deploy/docker/Dockerfile $(FLEETDB_SRC)
	docker build $(LOOM_BUILD_ARGS) -t $(REGISTRY)/loom:$(TAG) -f $(REPO_ROOT)/test/local-mode/Dockerfile $(REPO_ROOT)
	@# Context is a tar of exactly the two paths the image needs, streamed on
	@# stdin. Building from the repo root instead makes the root .dockerignore
	@# apply, and it excludes internal/webui/frontend/dist/ -- the very bundle
	@# this image exists to serve -- so the COPY fails however recently you ran
	@# build-frontend. A stdin tar carries no ignore file, so nothing is dropped.
	tar -C $(REPO_ROOT) -cf - internal/webui/frontend/dist deploy/terraform/ui \
	  | docker build -t $(REGISTRY)/loom-ui:$(TAG) -f deploy/terraform/ui/Dockerfile -
	docker push $(REGISTRY)/fleet-db:$(FLEETDB_TAG)
	docker push $(REGISTRY)/loom:$(TAG)
	docker push $(REGISTRY)/loom-ui:$(TAG)

# NAME selects a Terraform WORKSPACE, not just resource names. Without this
# every stack shares one state file in this directory: applying NAME=b after
# NAME=a makes Terraform read b as edits to a, and `make down NAME=b` destroys
# stack a. State lives in terraform.tfstate.d/<name>/.
#
# One caveat this does not fix: the local backend keeps a single lock, so two
# applies from this directory at once still serialise.
state: ## Select this stack's Terraform workspace (creates it on first use)
	@$(TF) init -input=false >/dev/null
	@$(TF) workspace select -or-create "$(NAME)" >/dev/null
	@echo "state: workspace $$($(TF) workspace show)"

apply: state ## Apply infrastructure only (assumes images already pushed)
	$(TF) apply -auto-approve -input=false $(TFVARS)

up: images apply ## Full pipeline: images -> infra -> wait healthy
	@echo "waiting for the stack to report healthy..."
	@$(CURDIR)/scripts/wait-healthy $(NAME) $(PROJECT) $(ZONE) \
		$$($(TF) output -raw api_port)
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
	@$(CURDIR)/scripts/tunnel $(NAME) $(PROJECT) $(ZONE) \
		$$($(TF) output -raw tunnel_ui_port) \
		$$($(TF) output -raw tunnel_api_port) \
		$$($(TF) output -raw tunnel_fleetdb_port) \
		$$($(TF) output -raw loom_workspace)

status: ## Container state on the VM
	gcloud compute ssh $(NAME) --project $(PROJECT) --zone $(ZONE) --tunnel-through-iap --quiet \
		--command 'sudo docker compose -f /opt/loom/docker-compose.yml ps; systemctl is-active loom-stack'

down: state ## Destroy this stack (bucket, HMAC key, secrets, VM, firewall)
	$(TF) destroy -auto-approve -input=false $(TFVARS)

fmt:
	$(TF) fmt -recursive

validate:
	$(TF) init -backend=false -input=false >/dev/null
	$(TF) validate

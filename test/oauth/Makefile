# Local OpenFGA + auth0-mock demo stack. Run from this directory, or use the
# root `make demo` / `make demo-down` which delegate here.
COMPOSE ?= docker compose

.PHONY: help up down logs seed demo

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

up: ## Start OpenFGA + auth0-mock
	$(COMPOSE) up -d --wait

down: ## Stop and remove the stack
	$(COMPOSE) down

logs: ## Tail the stack logs
	$(COMPOSE) logs -f

seed: ## Seed dev/staging/prod (1 model, 100 tuples, 100 assertions each)
	./seed.sh

demo: up ## Start the stack and seed the demo data
	@echo "› waiting for auth0-mock to be ready"
	@for i in $$(seq 1 30); do \
		curl -sf http://localhost:3000/.well-known/openid-configuration >/dev/null 2>&1 && break || sleep 1; \
	done
	@./seed.sh
	@printf '\nDemo ready. Point the CLI at it with an isolated config:\n\n'
	@printf '  export XDG_CONFIG_HOME=$$(mktemp -d)\n'
	@printf '  ofga profiles add local --api-url http://localhost:8080 && ofga profiles use local\n'
	@printf '  ofga profiles set auth_method client_credentials\n'
	@printf '  ofga profiles set client_id demo\n'
	@printf "  printf 'demo-secret' | ofga profiles set client_secret --value-stdin\n"
	@printf '  ofga profiles set token_url http://localhost:3000/oauth/token\n'
	@printf '  ofga profiles set audience https://api.openfga.local/\n'
	@printf '  ofga            # launch the TUI, then open API Logs (press 8)\n\n'

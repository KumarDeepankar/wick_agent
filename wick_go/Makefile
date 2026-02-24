.PHONY: build-server build-ui build dev clean

# Build the Go server binary via wick_deep_agent
build-server:
	python -c "from wick_deep_agent import WickServer; WickServer.build()"

# Install UI deps and build into static/
build-ui:
	cd ui && npm install && npm run build

# Build both server and UI
build: build-server build-ui

# Dev mode: start Go server + Vite dev server (hot reload)
dev: build-server
	python app.py &
	cd ui && npm install && npx vite --port 3000

# Production: build everything and run
prod: build
	python app.py

# Clean build artifacts
clean:
	rm -rf static/
	rm -rf ui/node_modules

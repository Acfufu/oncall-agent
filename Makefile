up:
	docker compose up -d
down:
	docker compose down
tidy:
	go mod tidy
run:
	go run ./cmd/server
check:
	curl -s http://localhost:8819/ping || true

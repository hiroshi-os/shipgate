.PHONY: test api demo-app hooks web demo tidy

test:
	cd api && go test ./...

tidy:
	cd api && go mod tidy

api:
	cd api && DEMO_SEED=true DEMO_CONTROLS=true \
		DEMO_APP_URL=http://localhost:8088 \
		ROLLBACK_SINK_URL=http://localhost:8090/hooks/rollback \
		DATABASE_PATH=../data/shipgate.db \
		LISTEN_ADDR=:8080 \
		go run ./cmd/shipgate

demo-app:
	cd demo-app && LISTEN_ADDR=:8088 go run .

hooks:
	cd hooks && LISTEN_ADDR=:8090 go run .

web:
	cd web && API_URL=http://localhost:8080 npm run dev

demo:
	@echo "Start demo-app, hooks, and api. In another terminal: make web"
	$(MAKE) -j3 demo-app hooks api

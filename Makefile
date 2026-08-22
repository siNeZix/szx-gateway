.PHONY: build run dev clean fmt tidy migrate-sqlite-to-mysql

BUILD_DIR=build
BINARY_NAME=gateway.exe
BINARY_PATH=$(BUILD_DIR)/$(BINARY_NAME)
SQLITE_PATH?=gateway.db

build:
	if not exist $(BUILD_DIR) mkdir $(BUILD_DIR)
	go build -o $(BINARY_PATH) cmd/gateway/main.go

run: build
	./$(BINARY_PATH)

dev: build
	powershell -NoProfile -ExecutionPolicy Bypass -Command "$$p = Start-Process -FilePath './$(BINARY_PATH)' -WorkingDirectory '.' -NoNewWindow -PassThru; try { for ($$i = 0; $$i -lt 60; $$i++) { if ($$p.HasExited) { throw 'gateway exited' }; try { $$c = [Net.Sockets.TcpClient]::new('127.0.0.1', 8080); $$c.Close(); break } catch { Start-Sleep -Milliseconds 500 } }; if ($$i -eq 60) { throw 'gateway did not open :8080' }; npm --prefix web run dev } finally { if ($$p -and -not $$p.HasExited) { Stop-Process -Id $$p.Id } }"

clean:
	if exist $(BUILD_DIR) rmdir /s /q $(BUILD_DIR)
	if exist gateway.db del gateway.db

fmt:
	go fmt ./...

tidy:
	go mod tidy

migrate-sqlite-to-mysql:
	go run ./cmd/migrate-sqlite-to-mysql -sqlite "$(SQLITE_PATH)" -mysql "$(DB_DSN)"

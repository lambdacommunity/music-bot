BINNAME=bot

all: build run-binary

build:
	go build -o $(BINNAME) ./cmd/music-bot/main.go 

run-binary: $(BINNAME)
	./$(BINNAME)

run:
	go run ./cmd/music-bot/main.go

export CGO_ENABLED=0

#Build linux
export GOOS=linux
export GOARCH=amd64
go build -o build/dnshield -ldflags "-w -s" ./cmd/dnshield/main.go
upx --best --lzma dnshield

#Build windows
export GOOS=windows
export GOARCH=amd64
go build -o build/dnshield.exe ./cmd/dnshield/main.go
#upx --best --lzma dnshield.exe
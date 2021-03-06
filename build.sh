export CGO_ENABLED=0

rm -rf build/

#Build linux
export GOOS=linux
export GOARCH=amd64
echo $GOOS $GOARCH
go build -o build/dnshield -ldflags "-w -s" ./cmd/dnshield/main.go
upx --best --lzma build/dnshield &>/dev/null

#BuildArm 32
export GOARCH=arm
echo $GOOS $GOARCH
go build -o build/dnshield_arm32 -ldflags "-w -s" ./cmd/dnshield/main.go
upx --best --lzma build/dnshield_arm32 &>/dev/null


#Build windows
export GOOS=windows
export GOARCH=amd64
echo $GOOS $GOARCH
go build -o build/dnshield.exe ./cmd/dnshield/main.go
upx --best --lzma build/dnshield.exe &>/dev/null
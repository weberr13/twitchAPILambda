download:
	@echo Download go.mod dependencies
	@go mod download

install-tools: download
	@echo Installing tools from tools.go
	go install github.com/smartystreets/goconvey
	go install golang.org/x/lint/golint
	go install golang.org/x/tools/cmd/goimports
	go install mvdan.cc/gofumpt
	go install github.com/golangci/golangci-lint/cmd/golangci-lint

vet: download
	@echo Vetting
	@go vet ./...
	@go mod tidy

vet-pipeline:
	@echo Vetting
	@go vet $$(go list -f {{.Dir}} ./... | grep -v /vendor/ | grep -v /.cache/pkg/mod/ | grep /twitchAPILambda/ )

lint: download legacy-lint golang-lint

legacy-lint: download
	@echo Running Linter
	@go install golang.org/x/lint/golint@latest
	@golint -set_exit_status ./...

legacy-lint-pipeline: 
	@echo Running Linter
	@golint -set_exit_status $$(go list -f {{.Dir}} ./... | grep -v /vendor/ | grep -v /.cache/pkg/mod/ | grep /twitchAPILambda/ )

golang-lint: download
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go mod tidy
	@golangci-lint run

golang-lint-pipeline:
	@golangci-lint run

fmt: download
	@echo formatting
	@go mod tidy
	@go install golang.org/x/tools/cmd/goimports@latest
	@go mod tidy
	@goimports -w ./
	@go install mvdan.cc/gofumpt@latest
	@go mod tidy
	@gofumpt -l -w ./

fmt-pipeline:
	@echo goimports
	@goimports -w $$(go list -f {{.Dir}} ./... | grep -v /vendor/ | grep -v /.cache/pkg/mod/ | grep /twitchAPILambda/ )
	@echo gofumpt
	@gofumpt -l -w $$(go list -f {{.Dir}} ./... | grep -v /vendor/ | grep -v /.cache/pkg/mod/ | grep /twitchAPILambda/ )

test: download
	@echo unit tests
	go test --count=1 $$(go list -f {{.Dir}} ./... | grep -v /vendor/ | grep -v /.cache/pkg/mod/ | grep /twitchAPILambda/ )

test-pipeline: 
	@echo unit tests
	go test --count=1 $$(go list -f '{{.Dir}}' ./... | grep -v /vendor/ | grep -v .cache)

integration-test: download
	@echo integration tests
	go test --tags=integration --count=1 $$(go list -f {{.Dir}} ./... | grep -v /vendor/ | grep -v /.cache/pkg/mod/ | grep /twitchAPILambda/ )

doc: download
	@echo creating godocs
	# @go get golang.org/x/tools/internal/gocommand@v0.4.0
	@go install golang.org/x/tools/cmd/godoc@latest
	@go install gitlab.com/tslocum/godoc-static@latest
	@godoc-static -link-index -destination=./docs ./
	@go mod tidy
	@csplit -f make-doc-tmp- docs/index.html /Packages/
	@cat make-doc-tmp-00 docs/index.html.in make-doc-tmp-01 > docs/index.html
	@rm make-doc-tmp-00 make-doc-tmp-01

doc-pipeline:
	@echo creating godocs
	@godoc-static -link-index -destination=./docs ./
	@csplit -f make-doc-tmp- docs/index.html /Packages/
	@cat make-doc-tmp-00 docs/index.html.in make-doc-tmp-01 > docs/index.html
	@rm make-doc-tmp-00 make-doc-tmp-01

build: download build-windows build-mac
	@echo building binaries
	@GOOS=linux GOARCH=amd64 go build -o main main.go
	@zip main.zip main

build-windows: download
	@echo building local windows 
	@GOOS=windows GOARCH=amd64 go build -o kukorohelp.exe ./cmd/kukoro
	@zip kukorohelp.zip kukorohelp.exe

build-mac: download
	@echo building local macos 
	@GOOS=darwin GOARCH=amd64 go build -o kukorohelp-darwin ./cmd/kukoro
	@zip kukorohelp-darwin.zip kukorohelp-darwin

all: download fmt vet lint test integration-test doc build build-windows

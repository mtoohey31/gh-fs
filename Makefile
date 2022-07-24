.PHONY: ci fmt fmt-check clean

gh-fs: go.mod go.sum **.go
	go build .

ci: gh-fs fmt-check

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

clean:
	rm -rf gh-fs result

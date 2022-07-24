.PHONY: ci release fmt fmt-check clean

gh-fs: go.mod go.sum **.go
	go build .

release: dist/freebsd-386 dist/freebsd-amd64 dist/freebsd-arm64 dist/linux-386 dist/linux-amd64 dist/linux-arm dist/linux-arm64 
	test -n "$(VERSION)" && gh auth status || exit 1
	git tag "v$(VERSION)"
	git push origin "v$(VERSION)"
	gh release create "v$(VERSION)"
	gh release upload "v$(VERSION)" $^

dist/freebsd-386:
	GOOS=freebsd GOARCH=386 go build -trimpath -ldflags="-s -w" -o $@
dist/freebsd-amd64:
	GOOS=freebsd GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $@
dist/freebsd-arm64:
	GOOS=freebsd GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $@
dist/linux-386:
	GOOS=linux GOARCH=386 go build -trimpath -ldflags="-s -w" -o $@
dist/linux-amd64:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $@
dist/linux-arm:
	GOOS=linux GOARCH=arm go build -trimpath -ldflags="-s -w" -o $@
dist/linux-arm64:
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $@

ci: gh-fs fmt-check

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

clean:
	rm -rf gh-fs result

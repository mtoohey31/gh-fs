.PHONY: ci release fmt fmt-check clean

SOURCE_FILES := go.mod go.sum **.go

gh-fs: $(SOURCE_FILES)
	go build .

RELEASE_FILES :=
GOOSS := freebsd linux
GOARCHES := 386 amd64 arm arm64
define ADD_PLATFORM =
RELEASE_FILES += dist/$1-$2
dist/$1-$2: $(SOURCE_FILES)
	GOOS=$1 GOARCH=$2 go build -trimpath -ldflags="-s -w" -o $$@
endef

$(foreach goos,$(GOOSS),$(foreach goarch,$(GOARCHES),$(eval $(call ADD_PLATFORM,$(goos),$(goarch)))))

release: $(RELEASE_FILES)
	test -n "$(VERSION)" && gh auth status || exit 1
	git tag "v$(VERSION)"
	git push origin "v$(VERSION)"
	gh release create "v$(VERSION)"
	gh release upload "v$(VERSION)" $^

ci: gh-fs fmt-check

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

clean:
	rm -rf dist gh-fs result

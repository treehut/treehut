VERSION ?= $(shell cat VERSION 2>/dev/null)
sources-tarball:
	mkdir -p dist/release
	echo $(VERSION) > VERSION
	sources=forgejo-src-$(VERSION).tar.gz ; tar --transform 's|^./|forgejo-src-$(VERSION)/|' -czf dist/release/forgejo-src-$(VERSION).tar.gz . ; cd dist/release ; shasum -a 256 $$sources > $$sources.sha256

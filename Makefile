.PHONY: build-all install-deps


install-deps:
	sudo apt install -y libnuma-dev libibverbs-dev rdma-core librdmacm-dev

build-all:
	git lfs install
	cd assets && git lfs fetch && git lfs checkout && tar -xzf nexus-benchmark-payload.tar.gz
	CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-server
	CGO_ENABLED=1 go build -tags rdma ./cmd/s3-rdma-smoke

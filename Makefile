UNAME := ${shell uname -s}
CMD_DIRS = $(shell find ./cmd/ -type d)
CMD_FILES = $(shell find ./cmd/ -type f -name '*')

test:
	echo "${UNAME}"

build: ./cmd ${CMD_DIRS} ${CMD_FILES}
	@echo "Build OS: ${UNAME}"
ifeq ($(UNAME),Darwin)
	@echo "Building ${version} for darwin..."
	go build -o emrys
	tar -czf emrys_${version}_darwin.tar.gz emrys
	gsutil cp emrys_${version}_darwin.tar.gz gs://emrys-public/clients/emrys_${version}_darwin.tar.gz
	mv emrys_${version}_darwin.tar.gz ../emrys/download/
endif
ifeq ($(UNAME),Linux)
	@echo "Building ${version} for linux..."
	go build -o emrys
	tar -czf emrys_${version}_linux.tar.gz emrys
	gsutil cp emrys_${version}_linux.tar.gz gs://emrys-public/clients/emrys_${version}.tar.gz
	gsutil cp emrys_${version}_linux.tar.gz gs://emrys-public/clients/emrys_${version}_linux.tar.gz
	mv emrys_${version}_linux.tar.gz ../emrys/download/
endif

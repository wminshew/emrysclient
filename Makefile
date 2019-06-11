DATE := $(shell date +%Y-%m-%d_%H-%M-%S)

dep-ensure:
	dep ensure -v

build-darwin:
	echo "Building ${version} for darwin...\n"
	go build -o emrys
	tar -czf emrys_${version}_darwin.tar.gz emrys
	gsutil cp emrys_${version}_darwin.tar.gz gs://emrys-public/clients/emrys_${version}_darwin.tar.gz
	mv emrys_${version}_darwin.tar.gz ../emrys/download/

build-linux:
	echo "Building ${version} for linux...\n"
	go build -o emrys
	tar -czf emrys_${version}_linux.tar.gz emrys
	gsutil cp emrys_${version}_linux.tar.gz gs://emrys-public/clients/emrys_${version}_linux.tar.gz
	mv emrys_${version}_linux.tar.gz ../emrys/download/

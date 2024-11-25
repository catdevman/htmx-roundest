build-roundestFunction:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bootstrap -ldflags '-extldflags "-static"'
	cp ./bootstrap ${ARTIFACTS_DIR}/.
	cp -r ./views ${ARTIFACTS_DIR}/views
